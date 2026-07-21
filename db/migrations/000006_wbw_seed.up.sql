-- ============================================================
-- WBW seed — ข้อมูลอ้างอิง (สำนักวิชา / กลุ่ม / ฐาน)
--
-- ⚠ school_id ระบุตรงๆ ไม่ปล่อยให้ SERIAL ไล่เอง
-- เพราะ frontend (web-next/components/register/mfu-data.ts) hardcode id ไว้
-- ของเดิมใน Express seed ปล่อย SERIAL → id ผูกกับลำดับ INSERT (เปราะ)
-- ============================================================

INSERT INTO school (school_id, name) VALUES
  (1,  'School of Agro-Industry'),
  (2,  'School of Cosmetic Science'),
  (3,  'School of Dentistry'),
  (4,  'School of Health Science'),
  (5,  'School of Applied Digital Technology'),
  (6,  'School of Integrative Medicine'),
  (7,  'School of Law'),
  (8,  'School of Liberal Arts'),
  (9,  'School of Management'),
  (10, 'School of Medicine'),
  (11, 'School of Nursing'),
  (12, 'School of Science'),
  (13, 'School of Sinology'),
  (14, 'School of Social Innovation')
ON CONFLICT (school_id) DO NOTHING;

-- ตั้ง sequence ต่อจาก id สูงสุด ไม่งั้น INSERT ถัดไปชนกับที่ระบุเอง
SELECT setval(pg_get_serial_sequence('school', 'school_id'), (SELECT MAX(school_id) FROM school));

-- ---------- กลุ่ม 40 กลุ่ม × 50 คน ----------
INSERT INTO participant_group (group_id, group_number, capacity)
SELECT g, g, 50 FROM generate_series(1, 40) AS g
ON CONFLICT (group_id) DO NOTHING;

SELECT setval(pg_get_serial_sequence('participant_group', 'group_id'), (SELECT MAX(group_id) FROM participant_group));

-- ---------- ฐานกิจกรรม 8 ฐาน (ต้องเช็คอิน) ----------
-- พิกัดฐาน 1 (วิหารพระเจ้าล้านทอง) มาจาก OpenStreetMap — จุดจริง
-- ฐานอื่นเป็นพิกัดโดยประมาณ ⚠ ต้องยืนยันพิกัดจริงกับผังงานก่อนวันงาน
INSERT INTO checkpoint (checkpoint_id, name, type, requires_checkin, activity_name, sequence, lat, lng) VALUES
  (1, 'วิหารพระเจ้าล้านทอง', 'activity', TRUE, 'ไหว้พระวิหารพระเจ้าล้านทอง', 1, 20.04148, 99.89658),
  (2, 'สวนกุหลาบ',           'activity', TRUE, 'R2L',                        2, 20.04390, 99.89900),
  (3, 'ลานวัฒนธรรม',         'activity', TRUE, 'ปลูกป่าข้ามแม่น้ำ',          3, 20.04680, 99.90120),
  (4, 'ลานสวนสน',            'activity', TRUE, 'ส่งแป้งข้ามหัว',             4, 20.04990, 99.90250),
  (5, 'จุดปลูก',             'activity', TRUE, 'ปลูกป่า',                    5, 20.05300, 99.90080),
  (6, 'ลานย่อย 3',           'activity', TRUE, 'ล้วงไห',                     6, 20.05460, 99.89780),
  (7, 'ฐานผ้าใบ',            'activity', TRUE, 'ผ้าใบเขียนความรู้สึก',       7, 20.05370, 99.89460),
  (8, 'ฐาน Zero Waste',      'activity', TRUE, 'Zero Waste และ SDGs',        8, 20.05120, 99.89290),
  -- ---------- จุดบริการ (ไม่ต้องเช็คอิน) ----------
  (9,  'MFU Botanical Garden',  'restroom',   FALSE, 'จุดห้องน้ำ',            NULL, 20.04750, 99.89400),
  (10, 'ลานย่อย 1',             'welfare',    FALSE, 'จุดสวัสดิการ',          NULL, 20.04550, 99.89750),
  (11, 'ลานย่อย 2',             'recreation', FALSE, 'สวัสดิการ/สันทนาการ',   NULL, 20.05200, 99.89900),
  (12, 'ทางออกฐานปลูกป่า',      'service',    FALSE, 'สวัสดิการ/รถห้องน้ำ',   NULL, 20.05380, 99.90170)
ON CONFLICT (checkpoint_id) DO NOTHING;

SELECT setval(pg_get_serial_sequence('checkpoint', 'checkpoint_id'), (SELECT MAX(checkpoint_id) FROM checkpoint));
