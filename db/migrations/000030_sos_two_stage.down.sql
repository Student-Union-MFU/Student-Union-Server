ALTER TABLE sos_event
  DROP COLUMN IF EXISTS escalated,
  DROP COLUMN IF EXISTS escalated_at,
  DROP COLUMN IF EXISTS escalated_by;
DROP TABLE IF EXISTS group_staff;
