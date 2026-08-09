-- โควตาการออกจากกลุ่ม — ผู้เข้าร่วมออกจากกลุ่มได้จำกัดจำนวนครั้ง กันย้ายกลุ่มไปมาจนกลุ่มไม่นิ่งก่อนวันงาน
--
-- คนที่ "มีกลุ่มอยู่แล้ว" ณ วันขึ้นระบบตั้งเป็น 0 ตั้งใจ — ถือว่าเขาเลือกจบไปแล้วก่อนกติกานี้จะมี
-- ถ้าตั้งเป็น 1 เท่ากันหมด ทุกคนจะได้สิทธิ์ย้ายเพิ่มฟรีหนึ่งครั้งพร้อมกันทั้งงาน ซึ่งตรงข้ามกับเป้าหมาย
-- คนที่ควรได้คืนเป็นราย ๆ ให้ admin ปรับให้ทีหลังได้
ALTER TABLE participant_profile
  ADD COLUMN leave_quota INT NOT NULL DEFAULT 1;

UPDATE participant_profile SET leave_quota = 0 WHERE group_id IS NOT NULL;

-- แยกจาก admin_log ตั้งใจ — admin_log เป็นบันทึกของ admin ถ้ายัดการเข้า/ออกของผู้เข้าร่วม 2,000 คน
-- ลงไป บันทึกของ admin จะถูกกลบจนใช้ไม่ได้ และ admin_log.detail เป็น TEXT ก้อนเดียว
-- ตอบคำถาม "คนนี้ออกกี่ครั้ง" ด้วย query ไม่ได้
CREATE TABLE group_membership_log (
  log_id      BIGSERIAL PRIMARY KEY,
  user_id     UUID NOT NULL REFERENCES wbw_user(user_id) ON DELETE CASCADE,
  -- กลุ่มที่เข้า/ที่ออกมา · NULL ได้เฉพาะ quota_adjust ตอนคนนั้นยังไม่มีกลุ่ม
  group_id    INT REFERENCES participant_group(group_id),
  action      TEXT NOT NULL,   -- 'join' | 'leave' | 'quota_adjust'
  -- สิทธิ์คงเหลือ "หลัง" ทำรายการ — เก็บทุกแถวเพื่อให้อ่านประวัติแล้วเห็นสถานะ ณ ตอนนั้นได้เลย
  -- ไม่ต้องไล่บวกลบย้อนจากแถวแรก
  quota_after INT NOT NULL,
  -- NULL = ผู้ใช้ทำเอง · มีค่า = admin คนนั้นเป็นคนทำให้
  actor_id    UUID REFERENCES wbw_user(user_id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- หน้ารายละเอียดผู้เข้าร่วมอ่านประวัติทีละคน เรียงใหม่ไปเก่า
CREATE INDEX idx_gml_user ON group_membership_log (user_id, log_id DESC);
