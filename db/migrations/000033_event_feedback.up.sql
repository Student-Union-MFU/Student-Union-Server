-- ความเห็นต่อ "การเดินทั้งงาน" ถามครั้งเดียวตอนครบทุกฐาน
--
-- ตารางใหม่ ไม่ใช่แถวใน checkin_feedback — ตารางนั้นมี checkpoint_id NOT NULL และผูก
-- FK ไปที่ checkpoint ความเห็นต่อเส้นทางไม่มีค่าที่ซื่อสัตย์จะใส่ลงช่องนั้น ทางเลี่ยงที่
-- เคยคิดคือยัดไว้กับฐานสุดท้าย ซึ่งทำให้ยอดของฐานสุดท้ายเพี้ยนไปตลอดกาลและไม่มีใครรู้ว่า
-- ทำไม
--
-- หนึ่งคนหนึ่งแถว: participant_id UNIQUE ไม่ใช่ (participant, อะไรอีกอย่าง) เพราะงานนี้
-- เดินปีละครั้งและความเห็นต่อการเดินมีได้ความเห็นเดียว ถ้าปีหน้าต้องแยกครั้ง ให้เพิ่ม
-- คอลัมน์ event/ปี แล้วย้าย UNIQUE ไปเป็นคู่ อย่าถอด UNIQUE ทิ้งเฉย ๆ
--
-- client_id UNIQUE ด้วยเหตุผลเดียวกับ checkin_feedback — ส่งซ้ำตอนเน็ตหลุดบนดอยต้องไม่
-- กลายเป็นสองความเห็น และ repository แยก 23505 สองสาเหตุออกจากกันด้วยคอลัมน์นี้
--
-- rating = ภาพรวมของการเดิน (บังคับ) · rating_activity = กิจกรรมตลอดเส้นทาง (ไม่บังคับ)
-- ชื่อ rating_activity ตรงกับคอลัมน์ใน checkin_feedback โดยตั้งใจ: เป็นคำถามเดียวกันที่
-- ย้ายที่ถาม ไม่ใช่คำถามใหม่ ฝั่งที่ทำสถิติจะได้ไม่ต้องจำสองชื่อ
CREATE TABLE IF NOT EXISTS event_feedback (
  id              BIGSERIAL PRIMARY KEY,
  participant_id  UUID NOT NULL UNIQUE REFERENCES wbw_user(user_id) ON DELETE CASCADE,
  rating          SMALLINT NOT NULL CHECK (rating BETWEEN 1 AND 5),
  rating_activity SMALLINT CHECK (rating_activity IS NULL OR rating_activity BETWEEN 1 AND 5),
  comment         TEXT,
  client_id       UUID NOT NULL UNIQUE,
  device_time     TIMESTAMPTZ NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
