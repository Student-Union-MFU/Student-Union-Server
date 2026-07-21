-- ============================================================
-- WBW (เดินรอบดอย / Walk Beyond the Wild) — schema
-- ย้ายมาจาก Express backend เดิม: WBW/backend/db/init/01_schema.sql
-- + migrations/001_add_missing_tables.sql + 002_notification_preset.sql
--
-- ตารางชุดนี้แยกจาก users/events/steps/leaderboard ของ su-server เดิม
-- (WBW ใช้ app_user เป็นของตัวเอง — login ด้วย student_id + password ไม่ใช่ OAuth)
-- ============================================================

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ---------- enum ----------
CREATE TYPE user_role       AS ENUM ('participant', 'staff', 'admin');
CREATE TYPE sex_type        AS ENUM ('male', 'female', 'unspecified');
CREATE TYPE blood_type      AS ENUM ('O-','O+','A-','A+','B-','B+','AB-','AB+');
CREATE TYPE checkpoint_type AS ENUM ('activity','restroom','welfare','recreation','service');
CREATE TYPE noti_level      AS ENUM ('info','warning','emergency');
CREATE TYPE audience_type   AS ENUM ('all','group','school','user');

-- ---------- sequence ----------
CREATE SEQUENCE bib_seq START 1;

-- ---------- ตารางที่ไม่มี FK ----------
CREATE TABLE school (
  school_id   SERIAL PRIMARY KEY,
  name        TEXT NOT NULL UNIQUE
);

CREATE TABLE participant_group (
  group_id      SERIAL PRIMARY KEY,
  group_number  INT NOT NULL UNIQUE,
  capacity      INT NOT NULL DEFAULT 50,
  member_count  INT NOT NULL DEFAULT 0,
  CONSTRAINT member_count_within_capacity CHECK (member_count <= capacity)
);

CREATE TABLE checkpoint (
  checkpoint_id     SERIAL PRIMARY KEY,
  name              TEXT NOT NULL,
  type              checkpoint_type NOT NULL DEFAULT 'activity',
  requires_checkin  BOOLEAN NOT NULL DEFAULT TRUE,
  activity_name     TEXT,
  sequence          INT,
  lat               DOUBLE PRECISION,
  lng               DOUBLE PRECISION
);

CREATE TABLE app_user (
  user_id       UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  username      TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  role          user_role NOT NULL DEFAULT 'participant',
  student_id    TEXT UNIQUE,
  display_name  TEXT,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---------- ตารางที่มี FK ----------
CREATE TABLE participant_profile (
  user_id                 UUID PRIMARY KEY REFERENCES app_user(user_id) ON DELETE CASCADE,
  first_name              TEXT,
  last_name               TEXT,
  date_of_birth           DATE,
  sex                     sex_type,
  contact_phone           TEXT,
  school_id               INT REFERENCES school(school_id),
  major                   TEXT,
  year                    INT,
  group_id                INT REFERENCES participant_group(group_id),
  bib_number              INT UNIQUE DEFAULT nextval('bib_seq'),
  qr_token                TEXT UNIQUE DEFAULT encode(gen_random_bytes(12),'hex'),
  emergency_contact_name  TEXT,
  emergency_contact_phone TEXT,
  photo_url               TEXT,
  event_start             TIMESTAMPTZ,
  checked_in              BOOLEAN NOT NULL DEFAULT FALSE,
  updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE checkpoint_staff (
  checkpoint_id INT  REFERENCES checkpoint(checkpoint_id) ON DELETE CASCADE,
  user_id       UUID REFERENCES app_user(user_id)         ON DELETE CASCADE,
  assigned_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (checkpoint_id, user_id)
);

CREATE TABLE admin_log (
  log_id     BIGSERIAL PRIMARY KEY,
  actor_id   UUID REFERENCES app_user(user_id) ON DELETE SET NULL,
  actor_name TEXT,
  action     TEXT NOT NULL,
  detail     TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE health_details (
  user_id              UUID PRIMARY KEY REFERENCES app_user(user_id) ON DELETE CASCADE,
  blood_type           blood_type,
  weight_kg            NUMERIC,
  height_cm            NUMERIC,
  food_allergies       TEXT,
  chronic_disease      TEXT,
  medications          TEXT,
  insurance            TEXT,
  physical_limitations TEXT,
  notes                TEXT,
  -- generated column — เขียนตรงๆ ไม่ได้
  has_medical_flag     BOOLEAN GENERATED ALWAYS AS (
                         food_allergies IS NOT NULL
                         OR chronic_disease IS NOT NULL
                         OR physical_limitations IS NOT NULL
                       ) STORED,
  updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE consent (
  user_id                     UUID PRIMARY KEY REFERENCES app_user(user_id) ON DELETE CASCADE,
  consent_health_data         BOOLEAN NOT NULL DEFAULT FALSE,
  consent_health_data_at      TIMESTAMPTZ,
  consent_emergency_treatment BOOLEAN NOT NULL DEFAULT FALSE,
  waiver_accepted             BOOLEAN NOT NULL DEFAULT FALSE,
  updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE check_in (
  id                  BIGSERIAL PRIMARY KEY,
  client_id           UUID NOT NULL UNIQUE,
  participant_id      UUID NOT NULL REFERENCES app_user(user_id),
  checkpoint_id       INT  NOT NULL REFERENCES checkpoint(checkpoint_id),
  staff_id            UUID REFERENCES app_user(user_id),
  device_time         TIMESTAMPTZ NOT NULL,
  server_received_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  lat                 DOUBLE PRECISION,
  lng                 DOUBLE PRECISION,
  CONSTRAINT uniq_participant_checkpoint UNIQUE (participant_id, checkpoint_id)
);

CREATE TABLE track_point (
  id             BIGSERIAL PRIMARY KEY,
  client_id      UUID NOT NULL UNIQUE,
  participant_id UUID NOT NULL REFERENCES app_user(user_id),
  device_time    TIMESTAMPTZ NOT NULL,
  lat            DOUBLE PRECISION,
  lng            DOUBLE PRECISION
);

CREATE TABLE sos_event (
  id                 BIGSERIAL PRIMARY KEY,
  client_id          UUID NOT NULL UNIQUE,
  participant_id     UUID NOT NULL REFERENCES app_user(user_id),
  device_time        TIMESTAMPTZ NOT NULL,
  server_received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  lat                DOUBLE PRECISION,
  lng                DOUBLE PRECISION,
  message            TEXT,
  resolved           BOOLEAN NOT NULL DEFAULT FALSE,
  resolved_by        UUID REFERENCES app_user(user_id),
  resolved_at        TIMESTAMPTZ
);

CREATE TABLE notification (
  id          BIGSERIAL PRIMARY KEY,
  type        TEXT NOT NULL DEFAULT 'announcement',
  title       TEXT NOT NULL,
  body        TEXT,
  level       noti_level    NOT NULL DEFAULT 'info',
  audience    audience_type NOT NULL DEFAULT 'all',
  audience_id TEXT,
  created_by  UUID REFERENCES app_user(user_id),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at  TIMESTAMPTZ
);

CREATE TABLE notification_read (
  notification_id BIGINT NOT NULL REFERENCES notification(id)  ON DELETE CASCADE,
  user_id         UUID   NOT NULL REFERENCES app_user(user_id) ON DELETE CASCADE,
  delivered_at    TIMESTAMPTZ,
  read_at         TIMESTAMPTZ,
  PRIMARY KEY (notification_id, user_id)
);

CREATE TABLE device_token (
  token      TEXT PRIMARY KEY,
  user_id    UUID NOT NULL REFERENCES app_user(user_id) ON DELETE CASCADE,
  platform   TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE group_message (
  id                 BIGSERIAL PRIMARY KEY,
  group_id           INT  NOT NULL REFERENCES participant_group(group_id),
  sender_id          UUID NOT NULL REFERENCES app_user(user_id),
  client_id          UUID NOT NULL UNIQUE,
  body               TEXT NOT NULL,
  device_time        TIMESTAMPTZ NOT NULL,
  server_received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- level/audience เป็น TEXT ไม่ใช่ enum (ตามของเดิม) — preset เก็บค่าที่ notification อาจ reject ได้
CREATE TABLE notification_preset (
  id          BIGSERIAL PRIMARY KEY,
  kind        TEXT NOT NULL DEFAULT 'preset',
  name        TEXT,
  title       TEXT,
  body        TEXT,
  level       TEXT NOT NULL DEFAULT 'info',
  audience    TEXT NOT NULL DEFAULT 'all',
  audience_id TEXT,
  created_by  UUID REFERENCES app_user(user_id) ON DELETE CASCADE,
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---------- index ----------
CREATE INDEX idx_checkin_participant ON check_in(participant_id);
CREATE INDEX idx_checkin_checkpoint  ON check_in(checkpoint_id);
CREATE INDEX idx_sos_unresolved      ON sos_event(resolved) WHERE resolved = FALSE;
CREATE INDEX idx_profile_group       ON participant_profile(group_id);
CREATE INDEX idx_profile_school      ON participant_profile(school_id);
CREATE INDEX idx_noti_audience       ON notification(audience, audience_id);
CREATE INDEX idx_device_token_user   ON device_token(user_id);
CREATE INDEX idx_group_message_group ON group_message(group_id, id);
CREATE INDEX idx_notif_preset_owner  ON notification_preset(created_by, kind);
CREATE UNIQUE INDEX uniq_notif_draft ON notification_preset(created_by) WHERE kind = 'draft';

-- ---------- trigger: นับสมาชิกกลุ่มอัตโนมัติ ----------
CREATE OR REPLACE FUNCTION sync_group_count() RETURNS TRIGGER AS $$
BEGIN
  IF TG_OP = 'UPDATE' AND NEW.group_id IS DISTINCT FROM OLD.group_id THEN
    IF OLD.group_id IS NOT NULL THEN
      UPDATE participant_group SET member_count = member_count - 1 WHERE group_id = OLD.group_id;
    END IF;
    IF NEW.group_id IS NOT NULL THEN
      UPDATE participant_group SET member_count = member_count + 1 WHERE group_id = NEW.group_id;
    END IF;
  ELSIF TG_OP = 'INSERT' AND NEW.group_id IS NOT NULL THEN
    UPDATE participant_group SET member_count = member_count + 1 WHERE group_id = NEW.group_id;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_group_count
AFTER INSERT OR UPDATE OF group_id ON participant_profile
FOR EACH ROW EXECUTE FUNCTION sync_group_count();
