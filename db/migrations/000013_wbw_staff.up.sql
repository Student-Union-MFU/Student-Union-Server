-- ============================================================
-- wbw_staff — ข้อมูลเจ้าหน้าที่ตอนสมัครเอง (/wbw/auth/staff-register)
--
-- ฟอร์มเดิมถาม "ชื่อที่แสดง" อย่างเดียว เปลี่ยนเป็นถาม สำนักวิชา / สาขา /
-- หน้าที่ในงาน แทน — display_name ยังอยู่ที่ wbw_user เหมือนเดิม
-- (ผู้ดูแลยังตั้งให้บัญชีที่สร้างเองผ่าน /admin/users ได้)
--
-- แยกเป็นตารางของตัวเองแบบเดียวกับ participant_profile:
-- participant ไม่มีแถวในนี้ และ staff ไม่มีแถวใน participant_profile
--
-- ------------------------------------------------------------
-- เดิมไฟล์นี้ชื่อ 000009_staff_profile และเลขชนกับ 000009_booth — พอเลขซ้ำ
-- golang-migrate เปิดโฟลเดอร์ไม่ได้เลย ("duplicate migration file") migration
-- ทั้งชุดจึงรันไม่ได้สักตัว ย้ายมาเป็น 000013 (11 กับ 12 ถูกใช้ใน feat/wbw-chat)
--
-- ตาราง staff_profile ถูกสร้างด้วยมือบนเครื่อง dev ตอนที่ migration ยังรันไม่ได้
-- คำสั่งข้างล่างจึงเขียนให้รันซ้ำได้: DB เปล่าจะ CREATE ส่วน DB ที่มีของเดิมอยู่
-- จะ RENAME ให้แทน ข้อมูลไม่หาย
-- ============================================================

-- CREATE TYPE ไม่มี IF NOT EXISTS
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'staff_role') THEN
    CREATE TYPE staff_role AS ENUM (
      'registration',  -- ลงทะเบียน
      'checkpoint',    -- ประจำฐาน
      'backstage',     -- เบื้องหลัง/เวที
      'security',      -- รักษาความปลอดภัย
      'medical',       -- พยาบาล/ปฐมพยาบาล
      'welfare',       -- สวัสดิการ/อาหาร
      'logistics',     -- สถานที่/อุปกรณ์
      'media',         -- สื่อ/ภาพถ่าย
      'guide',         -- พี่เลี้ยงกลุ่ม
      'other'          -- อื่น ๆ
    );
  END IF;
END $$;
-- เพิ่มหน้าที่ใหม่ทีหลัง: ALTER TYPE staff_role ADD VALUE 'xxx';
-- (ต้องเติมใน STAFF_ROLES ฝั่งเว็บ + validStaffRoles ฝั่ง Go ให้ตรงกันด้วย)

-- ของเดิมที่สร้างด้วยมือ: เปลี่ยนชื่อแทนการ DROP เพื่อไม่ให้ข้อมูลหาย
DO $$
BEGIN
  IF to_regclass('public.staff_profile') IS NOT NULL
     AND to_regclass('public.wbw_staff') IS NULL THEN
    ALTER TABLE staff_profile RENAME TO wbw_staff;
    ALTER TABLE wbw_staff RENAME CONSTRAINT staff_profile_pkey           TO wbw_staff_pkey;
    ALTER TABLE wbw_staff RENAME CONSTRAINT staff_profile_user_id_fkey   TO wbw_staff_user_id_fkey;
    ALTER TABLE wbw_staff RENAME CONSTRAINT staff_profile_school_id_fkey TO wbw_staff_school_id_fkey;
  END IF;
END $$;

CREATE TABLE IF NOT EXISTS wbw_staff (
  user_id     UUID PRIMARY KEY REFERENCES wbw_user(user_id) ON DELETE CASCADE,
  school_id   INT REFERENCES school(school_id),
  major       TEXT,
  staff_role  staff_role NOT NULL DEFAULT 'other',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- บัญชี staff/admin ที่มีอยู่ก่อนหน้า (ผู้ดูแลสร้างเอง) ไม่ต้อง backfill —
-- ทุก query ใช้ LEFT JOIN, ไม่มีแถว = ยังไม่ระบุ
