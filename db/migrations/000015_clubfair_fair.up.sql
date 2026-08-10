-- ============================================================
-- Club Fair — the fair itself.
--
-- 000009 gave the fair its booths and 000014 gave it its students. What was
-- missing is everything that happens between the two: the stamp a student
-- collects at a booth, the channel the Student Union talks to them through, and
-- the prizes the stamps add up to. Without the first of those the app has no
-- feature — a student's progress lived only in that phone's DataStore, so a
-- reinstall lost the afternoon and nothing could ever rank one student against
-- another.
--
-- ⚠ `check_in` already exists and is **not** this. That one is Walk-Bike-Week's:
-- it references wbw_user, carries lat/lng and a staff id, and one row means a
-- participant reached a GPS checkpoint on a route. Reusing it would have meant a
-- nullable FK to two different user tables. Hence `clubfair_checkin`.
--
-- Naming: singular, matching `booth`, `check_in`, `checkpoint`, `notification`
-- and the rest of the recent tables. `clubfair_users` (000014) is the odd one
-- out and is left alone rather than renamed — a rename costs every future
-- reference to it for a consistency nobody reads the schema for.
-- ============================================================

-- ------------------------------------------------------------
-- What the booths were missing.
--
-- `booth` holds a name, a category and its HMAC secret. The app renders three
-- things it does not have. All three are nullable and unpopulated: they need
-- copy and a floor plan that only the Student Union has, and a fabricated blurb
-- in a real database is worse than an empty column. Nullable also keeps the
-- existing iOS client's decode intact — it is a column it will not ask for.
-- ------------------------------------------------------------

-- One line on what the club actually does — the reason to walk over. A booth
-- list that is 28 names tells a student nothing about which queue to join.
ALTER TABLE booth ADD COLUMN IF NOT EXISTS about TEXT;

-- What is printed on the stall itself, which is not necessarily its id. The
-- seed in 000010 assigned ids 1–28 in document order; the physical numbering is
-- decided when the floor is laid out. Until this is set, id is the only number
-- there is.
ALTER TABLE booth ADD COLUMN IF NOT EXISTS display_number INT;

-- The area of the floor a booth stands in.
--
-- ⚠ This is *not* `category`, and the difference is a live decision. `category`
-- is what kind of club it is (5 values, from club.pdf, real). `zone` is where it
-- physically stands. The mobile app currently navigates by three themed areas —
-- Rainforest / Savannah / Deep Ocean — which are a design device with no data
-- behind them, while the server has real categories with no floor plan behind
-- them. Deliberately free text with no CHECK: constraining it to the app's three
-- placeholder habitats would bake a mockup into the schema.
ALTER TABLE booth ADD COLUMN IF NOT EXISTS zone TEXT;

CREATE INDEX IF NOT EXISTS idx_booth_zone ON booth (zone) WHERE zone IS NOT NULL;

-- ------------------------------------------------------------
-- What the students were missing.
--
-- Sign-up asks for both and the profile screen renders both. `users` has them;
-- `clubfair_users` was written without them, so the app had two fields it could
-- collect and nowhere to put.
-- ------------------------------------------------------------

ALTER TABLE clubfair_users ADD COLUMN IF NOT EXISTS school VARCHAR(255);
ALTER TABLE clubfair_users ADD COLUMN IF NOT EXISTS major  VARCHAR(255);

-- ============================================================
-- The stamp rally.
-- ============================================================

CREATE TABLE clubfair_checkin (
    id           BIGSERIAL PRIMARY KEY,

    -- Idempotency key, minted on the phone before the request goes out. Copied
    -- from WBW's `check_in`, and for the same reason: the app records a scan
    -- locally the instant the camera reads it and uploads later, so a retry
    -- after a timeout, a background sync and a reinstall-from-backup can all
    -- replay the same scan. UNIQUE makes the second attempt a no-op instead of
    -- a second stamp, without the client having to know whether the first one
    -- landed.
    client_id    UUID NOT NULL UNIQUE,

    user_id      INTEGER NOT NULL REFERENCES clubfair_users(id) ON DELETE CASCADE,
    booth_id     INTEGER NOT NULL REFERENCES booth(id) ON DELETE CASCADE,

    -- When the phone says it happened, and when the server actually heard. Both,
    -- because they diverge by however long the student was out of signal in a
    -- concrete hall, and only the second is trustworthy. Disputes over a prize
    -- draw closing at 17:00 sharp get settled on server_received_at.
    device_time        TIMESTAMPTZ NOT NULL,
    server_received_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- One stamp per booth per student. This is the constraint the whole feature
    -- rests on: the client already refuses a re-scan, but the client is a phone
    -- the student owns, so the rule has to live here as well.
    CONSTRAINT clubfair_checkin_once_per_booth UNIQUE (user_id, booth_id)
);

-- "How many booths has this student done", the app's most frequent read. The
-- UNIQUE above indexes (user_id, booth_id) which already serves it, so this
-- covers the other direction — a booth's own visitor count.
CREATE INDEX idx_clubfair_checkin_booth ON clubfair_checkin (booth_id);

-- Ordered replay of a student's afternoon, and the ordering the sync backlog
-- goes up in.
CREATE INDEX idx_clubfair_checkin_user_time
    ON clubfair_checkin (user_id, server_received_at);

-- ============================================================
-- The announcements channel.
--
-- Not `events`. That table is the SU-wide events listing — a title, a date and
-- a time as VARCHAR, no author and no way to react — and it is read by the
-- website. This is a chat channel: ordered posts from the Student Union that
-- students can only answer with an emoji.
-- ============================================================

CREATE TABLE clubfair_announcement (
    id         BIGSERIAL PRIMARY KEY,

    -- Who posted, kept even if their account goes: an announcement is a record
    -- of what the fair was told, so ON DELETE SET NULL rather than CASCADE.
    author_id  INTEGER REFERENCES clubfair_users(id) ON DELETE SET NULL,

    body       TEXT NOT NULL CHECK (length(btrim(body)) > 0),

    -- An instant, never a formatted string. `events.date`/`events.time` are
    -- VARCHAR(50) and cannot be rendered in Thai, in a 24-hour locale, or
    -- correctly after midnight. The client formats this at draw time.
    posted_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Soft delete. A post that has been reacted to and read by two thousand
    -- students should stop being shown rather than vanish from the record.
    deleted_at TIMESTAMPTZ
);

-- The channel is read newest-last and almost always unfiltered, so the only
-- index worth having is the one that skips deleted rows.
CREATE INDEX idx_clubfair_announcement_posted
    ON clubfair_announcement (posted_at) WHERE deleted_at IS NULL;

CREATE TABLE clubfair_announcement_reaction (
    id              BIGSERIAL PRIMARY KEY,
    announcement_id BIGINT  NOT NULL REFERENCES clubfair_announcement(id) ON DELETE CASCADE,
    user_id         INTEGER NOT NULL REFERENCES clubfair_users(id) ON DELETE CASCADE,

    -- The emoji itself rather than an id into a lookup table. The palette is
    -- five fixed characters chosen in the client; a table would be five rows
    -- and a join for something that never gets renamed. Bounded so this cannot
    -- become a free-text column: a few codepoints plus a variation selector or
    -- a ZWJ sequence fits, a sentence does not.
    emoji           TEXT NOT NULL CHECK (length(emoji) BETWEEN 1 AND 16),

    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- A student may leave several different reactions on one post but only one
    -- of each. This is what makes the toggle idempotent — the client sends the
    -- same tap twice on a flaky connection and the count stays right.
    CONSTRAINT clubfair_reaction_once
        UNIQUE (announcement_id, user_id, emoji)
);

-- Counting a post's reactions, which is every read of the channel.
CREATE INDEX idx_clubfair_reaction_announcement
    ON clubfair_announcement_reaction (announcement_id);

-- ============================================================
-- Prizes.
--
-- The thresholds live here rather than in the app, which is the point of the
-- table: the client currently hardcodes 10 / 20 / 27, so moving a threshold
-- mid-fair — because a booth pulled out, or the draw is undersubscribed at
-- four o'clock — would need a Play Store release. It is a handful of rows.
-- ============================================================

CREATE TABLE clubfair_prize_tier (
    id          SERIAL PRIMARY KEY,

    -- Stamps needed. UNIQUE because two tiers at the same count is a data entry
    -- error, not a scheme.
    threshold   INT NOT NULL UNIQUE CHECK (threshold > 0),

    name        TEXT NOT NULL,
    description TEXT,

    -- Lets a tier be retired without deleting the claims that point at it.
    is_active   BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE clubfair_prize_claim (
    id         BIGSERIAL PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES clubfair_users(id) ON DELETE CASCADE,
    tier_id    INTEGER NOT NULL REFERENCES clubfair_prize_tier(id) ON DELETE RESTRICT,

    -- Who handed it over. A prize is a physical object leaving a table, so the
    -- row that says it was collected has to say who collected it *from*.
    issued_by  INTEGER REFERENCES clubfair_users(id) ON DELETE SET NULL,
    claimed_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- The whole reason this table exists: one claim per tier per student, so a
    -- prize cannot be collected twice by walking to a second table.
    CONSTRAINT clubfair_prize_claim_once UNIQUE (user_id, tier_id)
);

-- ------------------------------------------------------------
-- The tiers the mobile client hardcodes today, so the two agree on day one.
-- 20 is the threshold the announcements copy already quotes to students.
-- ------------------------------------------------------------

INSERT INTO clubfair_prize_tier (threshold, name, description) VALUES
    (10, 'Halfway',    'Ten booths visited'),
    (20, 'Prize draw', 'Twenty booths — entry into the prize draw'),
    (28, 'Full sweep', 'Every booth at the fair')
ON CONFLICT (threshold) DO NOTHING;
