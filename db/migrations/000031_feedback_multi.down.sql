ALTER TABLE checkin_feedback
  DROP COLUMN IF EXISTS rating_scenery,
  DROP COLUMN IF EXISTS rating_activity,
  DROP COLUMN IF EXISTS rating_staff;

-- กลับไปสเกล 1-3 · แถวที่ให้ 4 หรือ 5 ไว้จะทำให้ CHECK ไม่ผ่าน ซึ่งตั้งใจให้ล้มดัง
-- แทนที่จะบีบค่าลงมาเงียบ ๆ แล้วเปลี่ยนคำตอบของคนอื่น
ALTER TABLE checkin_feedback
  DROP CONSTRAINT IF EXISTS checkin_feedback_rating_check;
ALTER TABLE checkin_feedback
  ADD CONSTRAINT checkin_feedback_rating_check CHECK (rating BETWEEN 1 AND 3);
