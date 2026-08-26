DROP INDEX IF EXISTS idx_group_message_moderated;
ALTER TABLE group_message
  DROP COLUMN IF EXISTS deleted_at,
  DROP COLUMN IF EXISTS deleted_by,
  DROP COLUMN IF EXISTS censored_at,
  DROP COLUMN IF EXISTS censored_by,
  DROP COLUMN IF EXISTS original_body;
