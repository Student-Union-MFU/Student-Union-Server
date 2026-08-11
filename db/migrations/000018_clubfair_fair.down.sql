-- Reverse of 000015. Children before parents, then the columns.
DROP TABLE IF EXISTS clubfair_prize_claim;
DROP TABLE IF EXISTS clubfair_prize_tier;
DROP TABLE IF EXISTS clubfair_announcement_reaction;
DROP TABLE IF EXISTS clubfair_announcement;
DROP TABLE IF EXISTS clubfair_checkin;

ALTER TABLE clubfair_users DROP COLUMN IF EXISTS major;
ALTER TABLE clubfair_users DROP COLUMN IF EXISTS school;

DROP INDEX IF EXISTS idx_booth_zone;
ALTER TABLE booth DROP COLUMN IF EXISTS zone;
ALTER TABLE booth DROP COLUMN IF EXISTS display_number;
ALTER TABLE booth DROP COLUMN IF EXISTS about;
