-- ============================================================
-- Club Fair — its own set of tables, all under a clubfair_ prefix.
--
-- The SU app's `users` was the obvious thing to reuse: same students, same
-- Google domain, and `steps`/`leaderboard` already point at it. It is not
-- reused, deliberately, and the reason is `oauth_subject NOT NULL UNIQUE` —
-- that table cannot hold a row that signed up any way other than Google
-- without either fabricating a `sub` or dropping the constraint that makes
-- every row a verified Google identity. Club Fair signs a student in with
-- Google and then has them set a password, so it needs a table where both
-- credentials are first-class.
--
-- The cost of that decision, stated here so nobody has to rediscover it: a
-- student who uses both apps is two rows with the same email, and the two can
-- disagree the moment either side edits a name. If Club Fair ever needs to
-- read a student's steps or leaderboard rank, this is where that hurts —
-- those FKs point at `users(id)`, not here. Migration 000012 renamed
-- app_user -> wbw_user over exactly this class of confusion; the prefix on
-- every table below is what keeps this set legible next to it.
-- ============================================================

CREATE TABLE clubfair_users (
    id            SERIAL PRIMARY KEY,

    -- Decides who may post to the announcements channel. Same three values as
    -- `users.user_type`, spelled as a CHECK rather than an ENUM: WBW's
    -- user_role enum needs a migration to gain a value, and a CHECK is the
    -- lighter of the two things to change.
    role          VARCHAR(20) NOT NULL DEFAULT 'student'
                  CHECK (role IN ('student', 'staff', 'admin')),

    -- Two columns, not one. `users.name` is a single string, which is why
    -- nothing over there can address a student by their given name alone or
    -- build initials from both halves. Sign-up asks for the two separately,
    -- so they are stored separately.
    first_name    VARCHAR(120) NOT NULL,
    surname       VARCHAR(120) NOT NULL,

    -- From the Google account. Unique because it is the identity a student
    -- signs in with, and the only field both credential paths share.
    email         VARCHAR(255) NOT NULL UNIQUE,

    -- Login takes a phone number, so it has to be unambiguous. Nullable and
    -- UNIQUE together are fine in Postgres: NULLs do not collide with each
    -- other, so any number of Google-only accounts can leave it empty while
    -- no two accounts can claim the same number.
    phone         VARCHAR(30) UNIQUE,

    -- Cut from the email local part (6831503029@lamduan.mfu.ac.th). UNIQUE
    -- here where `users.student_id` is not: it is derived from an already
    -- unique address, so a duplicate means something upstream is wrong and
    -- the database should say so rather than store both.
    student_id    VARCHAR(30) UNIQUE,
    avatar_url    TEXT,

    -- Google's `sub`. Stable across a name or address change, which is what
    -- makes it the identity rather than the email.
    oauth_subject VARCHAR(255) UNIQUE,
    -- bcrypt, cost 10, matching cmd/createadmin. Never returned by any
    -- endpoint — the model that serves this table must leave it off the
    -- response struct entirely, the way PublicBooth omits a booth's secret.
    password_hash TEXT,

    -- Misconduct flag, carried over from `users.is_flagged` so the two tables
    -- answer the same question the same way.
    is_flagged    BOOLEAN NOT NULL DEFAULT FALSE,

    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Both credentials are nullable, but a row with neither is an account
    -- nobody can ever sign in to. Sign-up writes oauth_subject first and the
    -- password second, so this permits the half-finished state in between
    -- while refusing the one that is simply broken.
    CONSTRAINT clubfair_users_has_credential
        CHECK (oauth_subject IS NOT NULL OR password_hash IS NOT NULL)
);

-- Login looks a student up by one of three things. Postgres builds an index
-- behind each UNIQUE above, so email, phone and student_id are already
-- covered; oauth_subject likewise. Nothing further is needed until a query
-- appears that filters on something else.
