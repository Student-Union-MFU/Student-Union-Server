-- ============================================================
-- The 28 clubs attending, from club.pdf.
--
-- ⚠ ids are given explicitly rather than left to SERIAL, matching
-- 000006_wbw_seed: the iOS client's category grouping relies on the document's
-- order, and an id that shifts with insert order is fragile.
-- ============================================================

INSERT INTO booth (id, name, category) VALUES
  (1,  'ชมรมเปตอง',        'sports'),
  (2,  'ชมรมเทควันโด',      'sports'),
  (3,  'ชมรมเคนโด้',        'sports'),
  (4,  'ชมรมว่ายน้ำ',        'sports'),
  (5,  'ชมรมมวยไทย',       'sports'),
  (6,  'ชมรมวอลเลย์บอล',    'sports'),
  (7,  'ชมรมแบดมินตัน',     'sports'),
  (8,  'ชมรมรักบี้',         'sports'),
  (9,  'ชมรมหมากกระดาน',   'sports'),
  (10, 'ชมรม BJJ',         'sports'),
  (11, 'ชมรมฟุตบอล',       'sports'),
  (12, 'ชมรมบาสเกตบอล',    'sports'),

  (13, 'ชมรมผู้นำเชียร์',     'student_relations'),
  (14, 'ชมรมสวัสดิการ',      'student_relations'),
  (15, 'ชมรมแม่ฟ้าคฑากร',   'student_relations'),
  (16, 'ชมรม DAC',         'student_relations'),

  (17, 'ชมรมอนุรักษ์พันธุ์สัตว์ป่าและป่าไม้', 'volunteer'),
  (18, 'ชมรมอาสาพัฒนาชุมชน',              'volunteer'),
  (19, 'ชมรมครูดอย',                     'volunteer'),

  (20, 'ชมรมพระพุทธศาสนา',     'religion_and_culture'),
  (21, 'ชมรมนาฏศิลป์',         'religion_and_culture'),
  (22, 'ชมรมคริสเตียน',        'religion_and_culture'),
  (23, 'ชมรมดนตรีไทย-พื้นเมือง', 'religion_and_culture'),

  (24, 'ชมรมการละคร',              'academic'),
  (25, 'ชมรมพิธีกร',                'academic'),
  (26, 'ชมรม MFUMUN',             'academic'),
  (27, 'ชมรม MFU Photo Club',     'academic'),
  (28, 'ชมรม MFU Human Rights Club', 'academic');

-- Explicit ids leave the sequence at 1, so the next booth anyone creates would
-- collide with id 1. This fails long after the migration, when nobody is
-- looking at it.
SELECT setval('booth_id_seq', (SELECT MAX(id) FROM booth));
