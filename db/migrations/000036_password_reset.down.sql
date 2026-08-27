DROP TABLE IF EXISTS wbw_password_reset;
DROP INDEX IF EXISTS wbw_user_email_key;
ALTER TABLE wbw_user DROP COLUMN IF EXISTS email;
