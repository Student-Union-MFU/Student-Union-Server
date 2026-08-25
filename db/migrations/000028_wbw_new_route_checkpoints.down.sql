-- กลับไปเป็นฐานชุดเดิมของเส้นทางวงกลม 8.36 กม. (ตรงกับ 000006_wbw_seed + 000007 i18n)
--
-- เหมือนขาขึ้น: ถ้ามี check_in อ้างฐานชุดใหม่อยู่ DELETE จะล้มเหลวเพราะ FK แบบ
-- RESTRICT ซึ่งตั้งใจ — rollback ที่ทำให้ข้อมูลเช็คอินหายไปเงียบ ๆ แย่กว่า rollback ที่ไม่ผ่าน
DELETE FROM checkpoint;

INSERT INTO checkpoint
  (checkpoint_id, name, type, requires_checkin, activity_name, sequence, lat, lng) VALUES
  (1, 'วิหารพระเจ้าล้านทอง', 'activity', TRUE, 'ไหว้พระวิหารพระเจ้าล้านทอง', 1, 20.04148, 99.89658),
  (2, 'สวนกุหลาบ',           'activity', TRUE, 'R2L',                        2, 20.04390, 99.89900),
  (3, 'ลานวัฒนธรรม',         'activity', TRUE, 'ปลูกป่าข้ามแม่น้ำ',          3, 20.04680, 99.90120),
  (4, 'ลานสวนสน',            'activity', TRUE, 'ส่งแป้งข้ามหัว',             4, 20.04990, 99.90250),
  (5, 'จุดปลูก',             'activity', TRUE, 'ปลูกป่า',                    5, 20.05300, 99.90080),
  (6, 'ลานย่อย 3',           'activity', TRUE, 'ล้วงไห',                     6, 20.05460, 99.89780),
  (7, 'ฐานผ้าใบ',            'activity', TRUE, 'ผ้าใบเขียนความรู้สึก',       7, 20.05370, 99.89460),
  (8, 'ฐาน Zero Waste',      'activity', TRUE, 'Zero Waste และ SDGs',        8, 20.05120, 99.89290),
  (9,  'MFU Botanical Garden', 'restroom',   FALSE, 'จุดห้องน้ำ',           NULL, 20.04750, 99.89400),
  (10, 'ลานย่อย 1',            'welfare',    FALSE, 'จุดสวัสดิการ',         NULL, 20.04550, 99.89750),
  (11, 'ลานย่อย 2',            'recreation', FALSE, 'สวัสดิการ/สันทนาการ',  NULL, 20.05200, 99.89900),
  (12, 'ทางออกฐานปลูกป่า',     'service',    FALSE, 'สวัสดิการ/รถห้องน้ำ',  NULL, 20.05380, 99.90170);

UPDATE checkpoint AS c SET
  name_en          = v.name_en,
  activity_name_en = v.activity_name_en
FROM (VALUES
  (1,  'Wihan Phra Chao Lan Thong', 'Pay respects at Wihan Phra Chao Lan Thong'),
  (2,  'Rose Garden',               'R2L'),
  (3,  'Culture Plaza',             'Plant trees across the river'),
  (4,  'Pine Grove Plaza',          'Pass the flour overhead'),
  (5,  'Planting Point',            'Tree planting'),
  (6,  'Sub-area 3',                'Reach into the jar'),
  (7,  'Canvas Base',               'Canvas: write your feelings'),
  (8,  'Zero Waste Base',           'Zero Waste & SDGs'),
  (9,  'MFU Botanical Garden',      'Restroom point'),
  (10, 'Sub-area 1',                'Welfare point'),
  (11, 'Sub-area 2',                'Welfare / recreation'),
  (12, 'Reforestation Base Exit',   'Welfare / mobile restroom')
) AS v(checkpoint_id, name_en, activity_name_en)
WHERE c.checkpoint_id = v.checkpoint_id;

SELECT setval(pg_get_serial_sequence('checkpoint', 'checkpoint_id'),
              (SELECT MAX(checkpoint_id) FROM checkpoint));
