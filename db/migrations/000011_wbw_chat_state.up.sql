-- แชท v2 — จุดตัดประวัติรายคน + cursor "อ่านถึงไหน"
--
-- since_id  : ข้อความ id <= ค่านี้ คนนี้ห้ามเห็น ตั้งใหม่ทุกครั้งที่กดเข้ากลุ่ม
--             (เข้ากลุ่มเดิมซ้ำ = ตัดประวัติใหม่อีกรอบ ตามที่ออกแบบไว้)
-- last_read_id : อ่านถึง id ไหนแล้ว เดินหน้าอย่างเดียว (GREATEST ตอน upsert)
-- read_at   : เวลาที่ยิง /chat/read ล่าสุด ใช้เป็น heartbeat "กำลังเปิดจอแชทอยู่"
--             push จะข้ามคนที่ read_at ใหม่กว่า 25 วินาที
--
-- ไม่มีแถว = เห็นประวัติทั้งหมด (since_id 0) — เกิดได้จริงเมื่อ admin ย้ายกลุ่ม
-- ให้ผู้ใช้ตรงๆ ผ่านหน้า /admin ซึ่งไม่ได้แตะตารางนี้เลย
CREATE TABLE group_chat_state (
  user_id      UUID   NOT NULL REFERENCES app_user(user_id) ON DELETE CASCADE,
  group_id     INT    NOT NULL REFERENCES participant_group(group_id) ON DELETE CASCADE,
  since_id     BIGINT NOT NULL DEFAULT 0,
  last_read_id BIGINT NOT NULL DEFAULT 0,
  read_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, group_id)
);

-- /chat/sync โหลด cursor ของทั้งกลุ่มทุกรอบ (ทุก 25 วิ ต่อคนที่เปิดแอปอยู่)
CREATE INDEX idx_chat_state_group ON group_chat_state(group_id);
