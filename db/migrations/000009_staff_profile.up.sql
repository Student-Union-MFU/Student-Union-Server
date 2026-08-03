-- ============================================================
-- staff_profile — ข้อมูลเจ้าหน้าที่ตอนสมัครเอง (/wbw/auth/staff-register)
--
-- ฟอร์มเดิมถาม "ชื่อที่แสดง" อย่างเดียว เปลี่ยนเป็นถาม สำนักวิชา / สาขา /
-- หน้าที่ในงาน แทน — display_name ยังอยู่ที่ app_user เหมือนเดิม
-- (ผู้ดูแลยังตั้งให้บัญชีที่สร้างเองผ่าน /admin/users ได้)
--
-- แยกเป็นตารางของตัวเองแบบเดียวกับ participant_profile:
-- participant ไม่มีแถวในนี้ และ staff ไม่มีแถวใน participant_profile
-- ============================================================

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
-- เพิ่มหน้าที่ใหม่ทีหลัง: ALTER TYPE staff_role ADD VALUE 'xxx';
-- (ต้องเติมใน STAFF_ROLES ฝั่งเว็บ + validStaffRoles ฝั่ง Go ให้ตรงกันด้วย)

CREATE TABLE staff_profile (
  user_id     UUID PRIMARY KEY REFERENCES app_user(user_id) ON DELETE CASCADE,
  school_id   INT REFERENCES school(school_id),
  major       TEXT,
  staff_role  staff_role NOT NULL DEFAULT 'other',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- บัญชี staff/admin ที่มีอยู่ก่อนหน้า (ผู้ดูแลสร้างเอง) ไม่ต้อง backfill —
-- ทุก query ใช้ LEFT JOIN, ไม่มีแถว = ยังไม่ระบุ
