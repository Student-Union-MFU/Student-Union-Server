-- จำกัดจำนวนผู้เข้าร่วมทั้งงานไว้ที่ 2,000 คน — นับเฉพาะ role = 'participant'
-- staff กับ admin ไม่นับ (เขาไม่ได้กินที่นั่งของผู้เข้าร่วม)
--
-- ทำไมบังคับที่ฐานข้อมูล ไม่ใช่ใน Go: ถ้าเช็ค `SELECT count(*)` แล้วค่อย INSERT
-- ตอนเปิดรับสมัครจะมีหลายสิบ request อ่านเลข 1,999 พร้อมกัน (READ COMMITTED
-- มองไม่เห็นแถวที่อีก transaction ยัง insert ไม่เสร็จ) แล้ว insert ทับกันจนเกินโควตา
-- แถว wbw_capacity แถวเดียวนี้ทำให้ทุกการสมัคร "ต่อคิว" ที่ row lock เดียวกัน
-- CHECK จึงเป็นเพดานที่ทะลุไม่ได้จริง ๆ
--
-- รูปแบบเดียวกับ participant_group (capacity/member_count + CHECK + trigger)
-- ที่ใช้คุมจำนวนคนต่อกลุ่มอยู่แล้ว
CREATE TABLE wbw_capacity (
  -- แถวเดียวเท่านั้น: PK เป็น TRUE ตายตัว แถวที่สองจึงชนคีย์เสมอ
  id               BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
  max_participants INT NOT NULL DEFAULT 2000 CHECK (max_participants >= 0),
  -- จำนวนที่ใช้ไปแล้ว · trigger ข้างล่างดูแลให้ตรงกับ wbw_user เสมอ
  taken            INT NOT NULL DEFAULT 0 CHECK (taken >= 0),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT taken_within_max CHECK (taken <= max_participants)
);

-- ตั้งต้นจากจำนวนคนที่สมัครไว้แล้ว (ตอนขึ้นระบบยังไม่ถึง 2,000 แน่ ๆ แต่เขียนให้ปลอดภัยไว้ก่อน
-- เผื่อรันบนฐานที่มีคนเยอะกว่านั้น — เพดานต้องไม่ต่ำกว่าของจริง ไม่งั้น CHECK พังตั้งแต่ INSERT นี้)
INSERT INTO wbw_capacity (max_participants, taken)
SELECT GREATEST(2000, count(*)), count(*) FROM wbw_user WHERE role = 'participant';

-- ---------- trigger: นับผู้เข้าร่วมอัตโนมัติ ----------
-- ผูกกับตาราง wbw_user ไม่ใช่กับ handler ตัวใดตัวหนึ่ง เพราะมีที่ INSERT ผู้ใช้อยู่หลายที่
-- (สมัครเอง, สร้างบัญชีให้จาก admin, cmd/createadmin) · ตั้งไว้ตรงนี้ที่เดียวจึงหลุดไม่ได้
--
-- ลบผู้เข้าร่วม = คืนที่นั่ง (admin ลบคนออกแล้วต้องมีคนอื่นสมัครแทนได้)
CREATE OR REPLACE FUNCTION sync_participant_count() RETURNS TRIGGER AS $$
DECLARE
  delta INT := 0;
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NEW.role = 'participant' THEN delta := 1; END IF;
  ELSIF TG_OP = 'DELETE' THEN
    IF OLD.role = 'participant' THEN delta := -1; END IF;
  ELSE  -- UPDATE: นับเฉพาะตอน role เปลี่ยนข้าง participant
    IF OLD.role <> 'participant' AND NEW.role = 'participant' THEN delta := 1;
    ELSIF OLD.role = 'participant' AND NEW.role <> 'participant' THEN delta := -1;
    END IF;
  END IF;

  IF delta <> 0 THEN
    UPDATE wbw_capacity SET taken = taken + delta, updated_at = now() WHERE id;
  END IF;

  IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_participant_count
AFTER INSERT OR DELETE OR UPDATE OF role ON wbw_user
FOR EACH ROW EXECUTE FUNCTION sync_participant_count();
