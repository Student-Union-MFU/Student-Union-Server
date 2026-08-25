-- ความเห็นต่อฐาน: จาก 1 คำถาม 3 ระดับ เป็น 4 คำถาม 5 ระดับ
--
-- `rating` เดิมยังอยู่และยังเป็น "ภาพรวม" เหมือนเดิม ไม่ได้เปลี่ยนความหมาย — หน้าแอดมิน
-- กับ CheckinProgressItem อ่านคอลัมน์นี้อยู่ ถ้าย้ายไปคอลัมน์ใหม่ทั้งหมดจะพังเงียบ ๆ
-- สามคอลัมน์ใหม่จึงเป็นส่วนขยาย ไม่ใช่การแทนที่
--
-- ทั้งสามเป็น NULL ได้ เพราะแถวที่ตอบไว้ก่อน migration นี้ตอบแค่ภาพรวม การบังคับ NOT NULL
-- แปลว่าต้องเดาค่าให้ข้อมูลเก่า ซึ่งเป็นการแต่งข้อมูลที่ไม่มีใครเคยตอบ
ALTER TABLE checkin_feedback
  DROP CONSTRAINT IF EXISTS checkin_feedback_rating_check;
ALTER TABLE checkin_feedback
  ADD CONSTRAINT checkin_feedback_rating_check CHECK (rating BETWEEN 1 AND 5);

ALTER TABLE checkin_feedback
  ADD COLUMN IF NOT EXISTS rating_scenery  SMALLINT CHECK (rating_scenery  IS NULL OR rating_scenery  BETWEEN 1 AND 5),
  ADD COLUMN IF NOT EXISTS rating_activity SMALLINT CHECK (rating_activity IS NULL OR rating_activity BETWEEN 1 AND 5),
  ADD COLUMN IF NOT EXISTS rating_staff    SMALLINT CHECK (rating_staff    IS NULL OR rating_staff    BETWEEN 1 AND 5);

-- ⚠ ค่าเดิมเป็นสเกล 1-3 อยู่ในคอลัมน์ที่ตอนนี้กว้าง 1-5 · ไม่แปลงค่าย้อนหลังโดยตั้งใจ:
-- "ชอบ" (3 จาก 3) ไม่ได้แปลว่า 5 จาก 5 และการคูณสเกลขึ้นคือการใส่ความเห็นให้คนที่ไม่ได้พูด
-- ฝั่งที่อ่านสถิติต้องดูวันที่ประกอบ ถ้าจะเทียบข้าม migration นี้
