-- ============================================================
-- Club Fair — the fair's own details, and its programme.
--
-- Two tables for two things the system has been getting from constants in
-- client code, which is the thing this schema keeps deciding not to do:
--
--  1. **When and where the fair is.** `FairSchedule` in the Android app and
--     `lib/fair.ts` on the website each hold their own copy of 22–23 August
--     2026, and the two have to be edited together by someone who remembers
--     both exist. An earlier pair of placeholder dates went past unnoticed and
--     every phone told students the fair had ended. Nothing serves this today:
--     `events.date`/`events.time` are VARCHAR(50) on the SU-wide listing and are
--     not this fair's window.
--
--  2. **What happens during it.** Opening, performances, the prize draw — the
--     Student Union has a running order and there has been nowhere to put it.
--
-- Both are read by the public website with no token, which is the point: a
-- student deciding whether to come is asking exactly these two questions.
--
-- ⚠ Not `events`, and not rows in it. That table is the SU-wide listing, read by
-- the SU website, guarded by SU auth and keyed to `users` — a different token
-- and a different user table. Putting the fair's programme there would mean the
-- Club Fair dashboard authenticating against two systems to edit one screen.
-- ============================================================

-- ------------------------------------------------------------
-- The fair itself. One row, forever.
--
-- `CHECK (id = 1)` rather than a table with no key: it makes a second row a
-- constraint violation instead of a silent ambiguity about which one is the
-- fair. The read is `SELECT ... WHERE id = 1` and the write is an UPSERT on the
-- same, so neither has to decide what "the current row" means.
--
-- Timestamps, not date and time strings. `events` stores VARCHAR(50) for both
-- and the consequence is that nothing over there can render a date in Thai, in
-- a 24-hour locale, or correctly across midnight. A TIMESTAMPTZ is an instant;
-- the clients format it. It also means the countdown on the website is arithmetic
-- rather than parsing.
-- ------------------------------------------------------------

CREATE TABLE clubfair_fair_info (
    id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),

    -- The window. Stored as instants and entered by staff in campus local time;
    -- the fair opens at 09:00 Asia/Bangkok whatever a visiting student's phone
    -- is set to.
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at   TIMESTAMPTZ NOT NULL,

    -- Thai is the original and English is the translation, NULL falling back to
    -- Thai — the convention 000007 set for checkpoint.name_en and 000019 kept
    -- for booth.name_en and clubfair_zone.name_en.
    venue    TEXT,
    venue_en TEXT,

    -- One line the Student Union can put on the front page without a release.
    -- Nullable and unseeded: there is nothing to say yet, and a placeholder in
    -- a real database is what the last set of placeholder dates was.
    notice    TEXT,
    notice_en TEXT,

    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Who last moved the dates. SET NULL rather than CASCADE: losing the staff
    -- account must not delete the fair.
    updated_by INTEGER REFERENCES clubfair_users(id) ON DELETE SET NULL,

    -- A fair that ends before it starts is a typo, and it is the typo that
    -- breaks every countdown reading this. Caught here so no client has to.
    CONSTRAINT clubfair_fair_info_window CHECK (ends_at > starts_at)
);

-- ------------------------------------------------------------
-- Seeded with what the two clients currently hardcode, so nothing changes on
-- the day this lands — the row exists to be edited, not to introduce a new
-- answer. 22 Aug 09:00 to 23 Aug 17:00, Asia/Bangkok, written as +07:00
-- because Thailand has had no daylight saving since 1920 and a fixed offset
-- states the intent more plainly than a timezone name would.
--
-- The venue is the campus and nothing more specific. The hall's own name is on
-- the Student Union's floor plan and has not been written down anywhere this
-- migration can read it, and a guessed building name on a page students
-- navigate by is worse than the campus alone.
-- ------------------------------------------------------------

INSERT INTO clubfair_fair_info (id, starts_at, ends_at, venue, venue_en) VALUES
    (1,
     '2026-08-22 09:00:00+07',
     '2026-08-23 17:00:00+07',
     'มหาวิทยาลัยแม่ฟ้าหลวง',
     'Mae Fah Luang University')
ON CONFLICT (id) DO NOTHING;

-- ------------------------------------------------------------
-- The programme.
--
-- Deliberately not tied to `booth`. Most of what happens at a fair happens on a
-- stage or at the entrance rather than at a stall, and an FK to a booth would
-- either be null on most rows or invite one to be invented for the opening
-- ceremony. `zone` is the optional link instead — it is where a thing happens,
-- which is what a student reading a running order wants.
-- ------------------------------------------------------------

CREATE TABLE clubfair_program (
    id SERIAL PRIMARY KEY,

    starts_at TIMESTAMPTZ NOT NULL,
    -- Nullable: "prize table opens at 15:00" has no stated end, and inventing
    -- one would put a closing time on a page that nobody promised.
    ends_at   TIMESTAMPTZ,

    title    TEXT NOT NULL CHECK (length(btrim(title)) > 0),
    title_en TEXT,

    detail    TEXT,
    detail_en TEXT,

    -- Free text rather than a zone, because most of these are a stage or a
    -- doorway. `zone` below is the structured half, for the ones that are in an
    -- area of the floor.
    location    TEXT,
    location_en TEXT,

    -- Optional. SET NULL rather than CASCADE for the same reason as above: a
    -- zone being renamed out of existence should not delete the schedule.
    zone CHAR(1) REFERENCES clubfair_zone(code) ON DELETE SET NULL,

    -- Draft by default. A running order is edited for weeks before it is true,
    -- and the public endpoint filters on this — so staff can build it in the
    -- dashboard without students reading a half-written schedule.
    is_published BOOLEAN NOT NULL DEFAULT FALSE,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT clubfair_program_window CHECK (ends_at IS NULL OR ends_at > starts_at)
);

-- The public read is "the published programme in running order", and that is
-- the only read there is. Partial, so the drafts staff are still writing cost
-- the index nothing.
CREATE INDEX idx_clubfair_program_running_order
    ON clubfair_program (starts_at) WHERE is_published;

-- No seed. The Student Union has not written the running order, and this table
-- exists so they can — an invented opening ceremony in a real database is a
-- claim about a real event.
