-- Back to the three tiers 000018 seeded, under their old names.
UPDATE clubfair_prize_tier
   SET threshold   = 10,
       name        = 'Halfway',
       description = 'Ten booths visited'
 WHERE threshold = 15;

UPDATE clubfair_prize_tier
   SET name        = 'Full sweep',
       description = 'Every booth at the fair'
 WHERE threshold = 28;

-- Re-seeded rather than reactivated: the up migration deletes this row outright
-- unless a claim held it in place. `ON CONFLICT` covers the case where it did —
-- then the row survived and only needs switching back on. Its id will differ
-- from the original after a delete, which is why the up migration moves the
-- other two rather than re-seeding them.
INSERT INTO clubfair_prize_tier (threshold, name, description) VALUES
    (20, 'Prize draw', 'Twenty booths — entry into the prize draw')
ON CONFLICT (threshold) DO NOTHING;

UPDATE clubfair_prize_tier
   SET is_active = TRUE
 WHERE threshold = 20;
