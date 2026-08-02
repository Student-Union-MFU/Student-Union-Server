-- ความเห็นต่อฐานที่เช็คอินแล้ว
--
-- ไม่มีคอลัมน์ "ยังไม่ตอบ" โดยตั้งใจ — pending = มีแถวใน check_in แต่ไม่มีแถวที่นี่
-- ตอบด้วย LEFT JOIN สถานะจึงเพี้ยนจากความจริงไม่ได้ตามนิยาม
CREATE TABLE IF NOT EXISTS checkin_feedback (
  id             BIGSERIAL PRIMARY KEY,
  participant_id UUID NOT NULL REFERENCES app_user(user_id)         ON DELETE CASCADE,
  checkpoint_id  INT  NOT NULL REFERENCES checkpoint(checkpoint_id) ON DELETE CASCADE,
  rating         SMALLINT NOT NULL CHECK (rating BETWEEN 1 AND 3), -- 1 ไม่ชอบ · 2 เฉยๆ · 3 ชอบ
  comment        TEXT,
  -- ส่งซ้ำตอนเน็ตหลุดต้องไม่เกิดแถวซ้ำ — ทรงเดียวกับ check_in และ group_message
  client_id      UUID NOT NULL UNIQUE,
  device_time    TIMESTAMPTZ NOT NULL,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT uniq_feedback_participant_checkpoint UNIQUE (participant_id, checkpoint_id)
);
CREATE INDEX IF NOT EXISTS idx_feedback_checkpoint ON checkin_feedback(checkpoint_id);

-- ทางเดียวที่หน้าแจ้งเตือนจะรู้ว่าแถวนี้ชี้ไปฐานไหน
-- ทางเลี่ยงคือ "ตอนแตะให้เดาจากฐานที่ยังไม่ตอบตัวเก่าสุด" ซึ่งพังทันทีที่มี 2 ฐานค้าง
ALTER TABLE notification ADD COLUMN IF NOT EXISTS ref_id TEXT;
