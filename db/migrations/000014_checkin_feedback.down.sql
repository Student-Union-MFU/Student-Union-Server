ALTER TABLE notification DROP COLUMN IF EXISTS ref_id;
DROP INDEX IF EXISTS idx_feedback_checkpoint;
DROP TABLE IF EXISTS checkin_feedback;
