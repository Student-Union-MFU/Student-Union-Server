ALTER TABLE wbw_user RENAME CONSTRAINT wbw_user_student_id_key TO app_user_student_id_key;
ALTER TABLE wbw_user RENAME CONSTRAINT wbw_user_username_key   TO app_user_username_key;
ALTER TABLE wbw_user RENAME CONSTRAINT wbw_user_pkey           TO app_user_pkey;

ALTER TABLE wbw_user RENAME TO app_user;
