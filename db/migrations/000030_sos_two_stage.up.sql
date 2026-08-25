-- SOS สองขั้น: เจ้าหน้าที่ประจำกลุ่มตรวจก่อน แล้วค่อยกระจายให้ทุกคน
--
-- เดิมเคสหนึ่งถูกยิงถึงเจ้าหน้าที่ตามฐาน (checkpoint_staff) ทันทีที่กด · ของจริงหน้างานคือ
-- แต่ละกลุ่มมีเจ้าหน้าที่เดินไปด้วย คนนั้นอยู่ใกล้ที่สุดและรู้ว่าเกิดอะไรขึ้นจริงหรือเปล่า
-- จึงให้เขาเห็นก่อนคนเดียว (บวกแอดมิน) แล้วถ้ายืนยันว่าเรื่องจริง ค่อย "ยกระดับ" ให้
-- เจ้าหน้าที่ทั้งงานเห็น
--
-- ⚠ แอดมินเห็นตั้งแต่ขั้นแรกเสมอ ตั้งใจ: ถ้าเจ้าหน้าที่ประจำกลุ่มแบตหมดหรือไม่ได้ดูจอ
-- เคสจะไม่จมหายไปโดยไม่มีใครรู้ — แอดมินคือตาข่ายรองรับ ไม่ใช่ตัวจับเวลา

-- ---------- เจ้าหน้าที่ประจำกลุ่ม ----------
--
-- รูปแบบเดียวกับ checkpoint_staff ทุกอย่าง: คนหนึ่งดูแลได้หลายกลุ่ม กลุ่มหนึ่งมีได้หลายคน
CREATE TABLE IF NOT EXISTS group_staff (
  group_id    INT  NOT NULL REFERENCES participant_group(group_id) ON DELETE CASCADE,
  user_id     UUID NOT NULL REFERENCES wbw_user(user_id)           ON DELETE CASCADE,
  assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (group_id, user_id)
);

-- หา "เคสนี้เป็นของเจ้าหน้าที่คนไหน" จาก group_id ของเคส ต้องเร็วเพราะอยู่ใน WHERE ของ feed
CREATE INDEX IF NOT EXISTS group_staff_user_idx ON group_staff (user_id);

-- ---------- ขั้นของเคส ----------
--
-- escalated = FALSE คือขั้นแรก (เฉพาะเจ้าหน้าที่ประจำกลุ่ม + แอดมิน)
-- escalated = TRUE  คือ SOS จริง กระจายถึงเจ้าหน้าที่ทุกคน
ALTER TABLE sos_event
  ADD COLUMN IF NOT EXISTS escalated    BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS escalated_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS escalated_by UUID REFERENCES wbw_user(user_id);

-- เคสที่มีอยู่ก่อน migration นี้ถือว่ายกระดับแล้ว — ตอนที่มันถูกสร้าง กติกาคือทุกคนเห็น
-- การเปลี่ยนกติกาย้อนหลังแล้วซ่อนเคสเก่าจากคนที่เคยเห็นอยู่ ไม่ใช่สิ่งที่ migration ควรทำ
UPDATE sos_event SET escalated = TRUE, escalated_at = COALESCE(acked_at, server_received_at)
 WHERE NOT escalated;
