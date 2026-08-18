-- ============================================================
-- Club Fair — booth owners.
--
-- A fourth role, and the table that says which booths it applies to.
--
-- ## Why a role and not a separate staff table
--
-- The obvious shape for "people who work the fair" is a table of their own, and
-- it was rejected: `clubfair_users` already carries the credential columns, the
-- unique keys on email/phone/student_id, the `has_credential` CHECK and both
-- sign-in paths. A second table would need every one of those again, plus a
-- second token audience and a second session — and the people staffing booths
-- are MFU students who already have an account from scanning at last year's
-- fair. Two rows for one person is how a stamp ends up on the wrong one.
--
-- So a booth owner is a `clubfair_users` row with `role = 'booth_owner'`, and
-- the thing that is genuinely new is the *assignment* below.
--
-- ## What the role may do, and what it deliberately may not
--
-- `booth_owner` reaches exactly one thing: the rotating check-in code for the
-- booths in `clubfair_booth_owner`. It is NOT `staff`:
--
--   * it cannot post an announcement to two thousand students,
--   * it cannot read the participant roster,
--   * it cannot hand out a prize,
--   * it cannot read another booth's code.
--
-- That list is the whole reason the role exists. Before it, the only credential
-- that could display a booth's QR was a staff token, so putting a screen on a
-- booth's table meant handing that booth's volunteers the announcements channel
-- and the participant list. `requireClubFairStaff` is unchanged and does not
-- admit this role — see cmd/main.go.
-- ============================================================

-- ------------------------------------------------------------
-- The role.
--
-- Dropped and re-added rather than altered: a CHECK constraint has no ALTER, and
-- 000017 chose VARCHAR + CHECK over an enum precisely so that widening it is
-- this and not an ALTER TYPE that cannot run inside a transaction.
--
-- The name is checked against `service.ClubFairRoleBoothOwner`. Two places spell
-- this string and they have to agree; the service-side switch in
-- UpdateParticipant is the other one.
-- ------------------------------------------------------------

ALTER TABLE clubfair_users DROP CONSTRAINT clubfair_users_role_check;

ALTER TABLE clubfair_users ADD CONSTRAINT clubfair_users_role_check
    CHECK (role IN ('student', 'staff', 'admin', 'booth_owner'));

-- ------------------------------------------------------------
-- Who owns which booth.
--
-- Many-to-many, and both directions matter:
--
--  * **A booth has several owners.** A booth is staffed for two days by people
--    working shifts. Modelling this as `booth.owner_id` would mean one person
--    holding the screen for sixteen hours, or the row being edited at each
--    handover — an edit that silently locks the previous shift out of a screen
--    they may still be standing in front of.
--
--  * **An owner has several booths.** Small clubs share volunteers, and the
--    person covering A4 at lunch is often the person covering A5.
--
-- No `is_primary`, no ordering, no shift times. All three were considered and
-- none has a consumer: the only question anything asks is "may this user see
-- this booth's code", which is a membership test. A shift schedule that nothing
-- reads is a schedule that drifts from what is actually happening in the hall.
-- ------------------------------------------------------------

CREATE TABLE clubfair_booth_owner (
    -- ON DELETE CASCADE both ways, and neither is a judgement call:
    -- an assignment to a deleted booth or a deleted account is not a row worth
    -- keeping, and a RESTRICT here would make deleting a booth fail for a
    -- reason nobody would guess from the message.
    booth_id INT NOT NULL REFERENCES booth(id) ON DELETE CASCADE,
    user_id  INT NOT NULL REFERENCES clubfair_users(id) ON DELETE CASCADE,

    -- Who assigned it and when. Not audit theatre: the question that gets asked
    -- during a fair is "why can this person see B12", and the answer is useless
    -- without a name against it. SET NULL rather than CASCADE, because the
    -- assignment outlives the admin who made it.
    assigned_by INT REFERENCES clubfair_users(id) ON DELETE SET NULL,
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- The pair is the identity. It also makes re-assigning the same booth to
    -- the same person an ON CONFLICT DO NOTHING rather than a duplicate row
    -- that would make the membership test return two answers.
    PRIMARY KEY (booth_id, user_id)
);

-- The membership test runs on every poll of a booth display — every thirty
-- seconds per screen, for two days — and it asks by user first: "which booths
-- may this person see". The primary key indexes (booth_id, user_id) and is no
-- use for that leading edge, so the reverse gets its own index.
CREATE INDEX clubfair_booth_owner_user_idx ON clubfair_booth_owner (user_id);
