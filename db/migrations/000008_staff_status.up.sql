-- ============================================================
-- staff self-registration + admin approval
--
-- เจ้าหน้าที่สมัครเองผ่าน /wbw/auth/staff-register → บัญชี role='staff'
-- สถานะ 'pending' รอผู้ดูแลอนุมัติก่อนถึงจะล็อกอินได้
--
-- participant (สมัครปกติ) และ staff/admin ที่ผู้ดูแลสร้างเองผ่าน /admin/users
-- ได้ 'approved' ทันที (default) — พฤติกรรมเดิมไม่เปลี่ยน
-- ============================================================

CREATE TYPE user_status AS ENUM ('pending', 'approved');

ALTER TABLE app_user
  ADD COLUMN status user_status NOT NULL DEFAULT 'approved';
