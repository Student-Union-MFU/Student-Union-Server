-- ย้อน migration 000005 — ลบเฉพาะของ WBW ไม่แตะ users/events/steps/leaderboard เดิม
DROP TRIGGER IF EXISTS trg_group_count ON participant_profile;
DROP FUNCTION IF EXISTS sync_group_count();

DROP TABLE IF EXISTS notification_preset;
DROP TABLE IF EXISTS group_message;
DROP TABLE IF EXISTS device_token;
DROP TABLE IF EXISTS notification_read;
DROP TABLE IF EXISTS notification;
DROP TABLE IF EXISTS sos_event;
DROP TABLE IF EXISTS track_point;
DROP TABLE IF EXISTS check_in;
DROP TABLE IF EXISTS consent;
DROP TABLE IF EXISTS health_details;
DROP TABLE IF EXISTS admin_log;
DROP TABLE IF EXISTS checkpoint_staff;
DROP TABLE IF EXISTS participant_profile;
DROP TABLE IF EXISTS app_user;
DROP TABLE IF EXISTS checkpoint;
DROP TABLE IF EXISTS participant_group;
DROP TABLE IF EXISTS school;

DROP SEQUENCE IF EXISTS bib_seq;

DROP TYPE IF EXISTS audience_type;
DROP TYPE IF EXISTS noti_level;
DROP TYPE IF EXISTS checkpoint_type;
DROP TYPE IF EXISTS blood_type;
DROP TYPE IF EXISTS sex_type;
DROP TYPE IF EXISTS user_role;
