-- ผู้ดูแลจัดการข้อความในแชทกลุ่มได้ — ลบ และเซ็นเซอร์
--
-- ทำไมเป็น soft delete ไม่ใช่ DELETE จริง สามเหตุผล และทั้งสามเป็นเหตุผลที่แยกจากกัน:
--
-- 1) id ของ group_message คือ cursor ของ long-poll ทั้งระบบ แอปถามว่า "ขอข้อความ
--    หลัง id นี้" ทุกรอบ · ลบแถวออกจริงไม่ได้ทำให้ cursor พัง (id ยังเรียงขึ้น
--    เสมอ) แต่ทำให้ประวัติที่สองเครื่องเห็นไม่ตรงกันถาวรโดยไม่มีใครรู้ว่าเคยมี
--    ข้อความอยู่ตรงนั้น
--
-- 2) แชทของงานเดินป่าเป็นหลักฐาน ถ้าเรื่องบานปลายหลังงาน (กลั่นแกล้ง ข่มขู่
--    นัดหมายที่ผิดกติกา) สิ่งที่ผู้จัดต้องตอบได้คือ "มีข้อความอะไร ใครเขียน
--    ใครลบ ลบตอนไหน" — DELETE ทำลายคำตอบนั้นทิ้งพร้อมกับตัวปัญหา
--
-- 3) กดลบผิดเป็นเรื่องปกติ และคนที่กดคือคนที่กำลังรีบ · กู้คืนได้แปลว่าความ
--    ผิดพลาดหนึ่งครั้งไม่ใช่ความเสียหายถาวร
--
-- เซ็นเซอร์เก็บของเดิมไว้ใน original_body แล้วเขียนข้อความแทนที่ลงใน body:
-- body เป็น "สิ่งที่คนอ่านเห็น" ที่เดียว ทุก endpoint ของแชทที่มีอยู่จึงเคารพ
-- การเซ็นเซอร์ทันทีโดยไม่ต้องแก้ query ไหนเลย — ถ้าแยกเป็นคอลัมน์ใหม่แล้วให้
-- แต่ละที่เลือกเอง วันหนึ่งจะมี endpoint ที่ลืมเลือกแล้วปล่อยของเดิมหลุดออกไป
ALTER TABLE group_message
  ADD COLUMN IF NOT EXISTS deleted_at    TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS deleted_by    UUID REFERENCES wbw_user(user_id),
  ADD COLUMN IF NOT EXISTS censored_at   TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS censored_by   UUID REFERENCES wbw_user(user_id),
  ADD COLUMN IF NOT EXISTS original_body TEXT;

-- ดัชนีสำหรับหน้าผู้ดูแลที่ถามว่า "มีอะไรถูกจัดการไปบ้าง" — partial index เพราะ
-- ข้อความที่ถูกแตะต้องเป็นส่วนน้อยมากของทั้งตาราง
CREATE INDEX IF NOT EXISTS idx_group_message_moderated
  ON group_message (group_id, id)
  WHERE deleted_at IS NOT NULL OR censored_at IS NOT NULL;
