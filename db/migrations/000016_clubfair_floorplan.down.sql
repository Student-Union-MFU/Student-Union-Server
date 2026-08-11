-- Reverse of 000016, back to the state 000015 left: a free-text zone and an
-- integer display_number, both empty.
DROP INDEX IF EXISTS idx_booth_zone_code;
ALTER TABLE booth DROP CONSTRAINT IF EXISTS booth_zone_fkey;

UPDATE booth SET zone = NULL, booth_code = NULL, name_en = NULL;

ALTER TABLE booth DROP COLUMN IF EXISTS name_en;
ALTER TABLE booth DROP COLUMN IF EXISTS booth_code;
ALTER TABLE booth ADD COLUMN IF NOT EXISTS display_number INT;
CREATE INDEX IF NOT EXISTS idx_booth_zone ON booth (zone) WHERE zone IS NOT NULL;

DROP TABLE IF EXISTS clubfair_zone;
