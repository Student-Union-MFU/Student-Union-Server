ALTER TABLE sos_event DROP CONSTRAINT IF EXISTS sos_event_severity_check;
ALTER TABLE sos_event
  DROP COLUMN IF EXISTS severity,
  DROP COLUMN IF EXISTS severity_at,
  DROP COLUMN IF EXISTS severity_by;
