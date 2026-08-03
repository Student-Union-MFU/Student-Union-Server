-- ============================================================
-- app_user -> wbw_user
--
-- "app_user" was the most generic name in the database attached to the least
-- generic table: it is not the app's user table, it is WBW's. The SU app has
-- its own, unrelated `users` (SERIAL id, Google OAuth) that steps and
-- leaderboard point at. Two tables called some variant of "user", holding
-- different keys for different people, is how someone eventually joins the
-- wrong one. The wbw_ prefix already marks every other file in this feature.
--
-- Postgres tracks foreign keys by OID, not by name, so all 17 inbound FKs
-- follow the rename with no work — nothing else in the schema changes.
-- The constraint renames below are cosmetic: a rename leaves them as
-- app_user_pkey etc., which would keep the old name alive in \d output and in
-- every future error message about a duplicate username.
-- ============================================================

ALTER TABLE app_user RENAME TO wbw_user;

ALTER TABLE wbw_user RENAME CONSTRAINT app_user_pkey            TO wbw_user_pkey;
ALTER TABLE wbw_user RENAME CONSTRAINT app_user_username_key    TO wbw_user_username_key;
ALTER TABLE wbw_user RENAME CONSTRAINT app_user_student_id_key  TO wbw_user_student_id_key;
