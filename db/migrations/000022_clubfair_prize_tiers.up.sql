-- ============================================================
-- Two prizes, named plainly.
--
-- 000018 seeded 10 / 20 / 28 to match what the mobile client hardcoded at the
-- time. The scheme the Student Union actually settled on is two: one at fifteen
-- booths, the halfway mark of a 28-booth fair, and one for all twenty-eight.
-- Three stops made the middle one look like the real target and left the run
-- from 20 to 28 as a long stretch with nothing on it.
--
-- The names are 'Prize 1' and 'Prize 2' and are meant to stay that way. Nobody
-- walking a fair needs the tiers editorialised at them, and the app already
-- prints the only two facts that matter next to each stop — the booth count and
-- how far off it is.
--
-- Nothing here is a schema change: this is the table doing the job it was
-- created for. See the note above `clubfair_prize_tier` in 000018 — the
-- thresholds live in rows precisely so moving one does not need an app release.
-- ============================================================

-- The first tier is moved, not replaced. Its id is what
-- `clubfair_prize_claim.tier_id` points at, so re-seeding it as a new row would
-- orphan every claim already handed out under it.
UPDATE clubfair_prize_tier
   SET threshold   = 15,
       name        = 'Prize 1',
       description = '15 booths visited'
 WHERE threshold = 10;

UPDATE clubfair_prize_tier
   SET name        = 'Prize 2',
       description = '28 booths visited'
 WHERE threshold = 28;

-- The draw is dropped outright — a tier nobody has collected is just a row.
--
-- Guarded rather than a bare DELETE because the claim table's FK is ON DELETE
-- RESTRICT, and rightly so: a student holding a prize issued under this tier has
-- a row that has to keep pointing somewhere. If any exist, the DELETE matches
-- nothing and the UPDATE below retires the tier instead, which takes it out of
-- `prizeTiers` (it filters on `is_active`) without destroying the record of who
-- was handed what.
DELETE FROM clubfair_prize_tier t
 WHERE t.threshold = 20
   AND NOT EXISTS (
       SELECT 1 FROM clubfair_prize_claim c WHERE c.tier_id = t.id
   );

UPDATE clubfair_prize_tier
   SET is_active = FALSE
 WHERE threshold = 20;
