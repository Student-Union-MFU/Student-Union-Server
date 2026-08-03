-- ============================================================
-- Booth — one row per club standing at the fair.
--
-- `secret` is the HMAC key behind that booth's rotating check-in QR. It is
-- generated here, with the row, and must never leave the server: whoever holds
-- it can mint valid check-in codes for that booth for the rest of the event.
-- No endpoint returns it until there is auth to decide who may see it.
-- ============================================================

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE booth (
    id         SERIAL PRIMARY KEY,
    -- Nullable, and no foreign key: booths exist before anyone decides which
    -- event they belong to, and the events table is empty. Inventing an event
    -- to point at would put a fabricated date in a real database.
    event_id   INTEGER,
    name       TEXT NOT NULL,
    -- Constrained to catch a typo at seed time. Adding a category later needs
    -- a migration; the iOS client already decodes an unrecognised value to
    -- `.unknown` rather than failing, so it will not break on one.
    category   TEXT NOT NULL CHECK (category IN (
                   'sports', 'student_relations', 'volunteer',
                   'religion_and_culture', 'academic')),
    secret     TEXT NOT NULL DEFAULT encode(gen_random_bytes(32), 'base64'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
