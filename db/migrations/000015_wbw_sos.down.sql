DROP INDEX IF EXISTS sos_feed_idx;
DROP INDEX IF EXISTS sos_one_open_per_user;

ALTER TABLE sos_event
  DROP COLUMN IF EXISTS for_other,
  DROP COLUMN IF EXISTS group_id,
  DROP COLUMN IF EXISTS checkpoint_id,
  DROP COLUMN IF EXISTS accuracy_m,
  DROP COLUMN IF EXISTS loc_source,
  DROP COLUMN IF EXISTS acked_at,
  DROP COLUMN IF EXISTS acked_by,
  DROP COLUMN IF EXISTS resolve_reason,
  DROP COLUMN IF EXISTS last_push_at,
  DROP COLUMN IF EXISTS updated_at;
