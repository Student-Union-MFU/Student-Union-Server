-- ระดับความรุนแรงของเคส SOS ที่เจ้าหน้าที่ประเมินหลังไปถึงตัวคน
--
-- ต่างจาก resolve_reason ตรงที่ "ยังไม่จบ" — resolve_reason เขียนตอนปิดเคส ส่วนคอลัมน์นี้
-- เขียนตอนเคสยังเปิดอยู่ เพื่อบอกว่าที่ไปเจอมาหนักแค่ไหน · เคสที่ถูกตีเป็น major/urgent
-- ยังค้างอยู่ในหน้า console ของทุกคนต่อไป ซึ่งเป็นเรื่องที่ตั้งใจ: สิ่งที่ยังต้องการคน
-- ไม่ควรหายไปจากจอเพราะมีคนไปถึงแล้ว
--
-- CHECK ไม่ใช่ ENUM เพราะระดับพวกนี้เป็นเรื่องของ "ขั้นตอนการทำงานหน้างาน" ที่ผู้จัดอาจ
-- อยากเพิ่ม/แก้ทีหลัง — ALTER TABLE ... DROP CONSTRAINT แล้วใส่ใหม่ ง่ายกว่าการแก้ ENUM
ALTER TABLE sos_event
  ADD COLUMN IF NOT EXISTS severity     TEXT,
  ADD COLUMN IF NOT EXISTS severity_at  TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS severity_by  UUID REFERENCES wbw_user(user_id);

ALTER TABLE sos_event
  DROP CONSTRAINT IF EXISTS sos_event_severity_check;
ALTER TABLE sos_event
  ADD CONSTRAINT sos_event_severity_check
  CHECK (severity IS NULL OR severity IN ('minor', 'major', 'urgent'));
