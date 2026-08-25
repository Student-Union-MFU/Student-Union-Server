-- เส้นทางใหม่ของงาน (newroute.gpx / Google Maps) — ฐานชุดใหม่ทั้งหมด
--
-- เส้นทางเดิมเป็นวงกลม 8.36 กม. กลับมาจบที่จุดเริ่ม · เส้นทางใหม่ยาว 5.13 กม.
-- เดินจากวิหารพระเจ้าล้านทองไปจบทางทิศตะวันออกเฉียงเหนือ ไม่ได้วนกลับ
--
-- พิกัดมาจากหมุดใน Google Maps ของผู้จัด (10 หมุด) ไม่ใช่จากไฟล์ GPX — ตัว GPX ที่
-- export ผ่าน GoogleMapsToGPX มีแต่ trkpt ของเส้นทาง ไม่มี wpt ของหมุดเลยสักอัน
--
-- หมุดที่ 1 ถึง 9 = ฐานที่สแกนเช็คอินได้ (requires_checkin) · หมุดสุดท้าย (10) คือ
-- "เส้นชัย" ปลายทางของเส้นทาง ไม่ใช่ฐาน จึงไม่เปิดให้เช็คอิน
--
-- ⚠ ชื่อฐาน: หมุด 3-9 ใน Google Maps เป็นหมุดเปล่าไม่มีชื่อ · ชื่อไทยที่ใส่ไว้ตรงนี้
-- ยกมาจากฐานชุดเดิมเรียงตามลำดับตามที่ผู้จัดสั่ง ไม่ได้มาจากพิกัด — วัดระยะแล้วฐานเดิม
-- แต่ละอันอยู่ห่างจากหมุดใหม่ 400 ม. ถึง 1 กม. ยกเว้นวิหารพระเจ้าล้านทองที่ตรงกันจริง
-- (ห่าง 11 ม.) · ถ้าชื่อไหนไม่ตรงกับสถานที่จริง แก้ได้ที่ /wbw/admin/checkpoints
--
-- เขียนทับแถวเดิมทีละ id แทนการ DELETE ทั้งตารางแล้ว INSERT ใหม่
--
-- เวอร์ชันแรกของ migration นี้ใช้ DELETE FROM checkpoint แล้วล้มทันที:
-- sos_event.checkpoint_id เป็น FK ที่อ้างฐานอยู่ (id 1 กับ 9) และเป็นแบบ RESTRICT
-- การ ON CONFLICT DO UPDATE ทำให้ id เดิมยังอยู่ครบ ไม่มี FK ไหนขาด และไม่ต้องลบ
-- ประวัติ SOS ทิ้งเพื่อย้ายฐาน
--
-- ⚠ id ถูกใช้ซ้ำกับสถานที่ใหม่ · sos_event เก่าที่ชี้ id 9 จะกลายเป็นชี้ "ฐาน Zero Waste"
-- แทน "MFU Botanical Garden" ซึ่งยอมรับได้เพราะฐานชุดเดิมไม่มีอยู่แล้วหลังเปลี่ยนเส้นทาง
INSERT INTO checkpoint
  (checkpoint_id, name, type, requires_checkin, activity_name, sequence, lat, lng) VALUES
  -- ---------- ฐานที่เช็คอินได้ (หมุด 1-9) ----------
  (1, 'วิหารพระเจ้าล้านทอง',  'activity', TRUE,  'ไหว้พระวิหารพระเจ้าล้านทอง', 1, 20.0414105, 99.8966591),
  (2, 'MFU Botanical Garden', 'activity', TRUE,  NULL,                        2, 20.0379012, 99.8960021),
  (3, 'สวนกุหลาบ',            'activity', TRUE,  'R2L',                       3, 20.0366246, 99.8994385),
  (4, 'ลานวัฒนธรรม',          'activity', TRUE,  'ปลูกป่าข้ามแม่น้ำ',          4, 20.0350223, 99.9000347),
  (5, 'ลานสวนสน',             'activity', TRUE,  'ส่งแป้งข้ามหัว',             5, 20.0354986, 99.9048270),
  (6, 'จุดปลูก',              'activity', TRUE,  'ปลูกป่า',                   6, 20.0445811, 99.9096338),
  (7, 'ลานย่อย 3',            'activity', TRUE,  'ล้วงไห',                    7, 20.0511027, 99.9095245),
  (8, 'ฐานผ้าใบ',             'activity', TRUE,  'ผ้าใบเขียนความรู้สึก',       8, 20.0531898, 99.9116660),
  (9, 'ฐาน Zero Waste',       'activity', TRUE,  'Zero Waste และ SDGs',       9, 20.0553452, 99.9091266),
  -- ---------- ปลายทาง (หมุด 10) — ไม่ใช่ฐาน ----------
  (10, 'เส้นชัย',             'service',  FALSE, 'จุดสิ้นสุดเส้นทาง',         NULL, 20.0567512, 99.9087037)
ON CONFLICT (checkpoint_id) DO UPDATE SET
  name             = EXCLUDED.name,
  type             = EXCLUDED.type,
  requires_checkin = EXCLUDED.requires_checkin,
  activity_name    = EXCLUDED.activity_name,
  sequence         = EXCLUDED.sequence,
  lat              = EXCLUDED.lat,
  lng              = EXCLUDED.lng;

-- ฐานเดิมที่เกินมา (11, 12 ของเส้นทางเก่า) · ไม่มีตารางไหนอ้างถึงอยู่ ลบได้ตรง ๆ —
-- ถ้าวันหนึ่งมี check_in อ้าง คำสั่งนี้จะล้มเหลวแทนที่จะลบข้อมูลเช็คอินทิ้งเงียบ ๆ
DELETE FROM checkpoint WHERE checkpoint_id > 10;

-- คำแปลอังกฤษ (คอลัมน์จาก 000007) — ยกตามชื่อไทยชุดเดียวกัน
UPDATE checkpoint AS c SET
  name_en          = v.name_en,
  activity_name_en = v.activity_name_en
FROM (VALUES
  (1,  'Wihan Phra Chao Lan Thong', 'Pay respects at Wihan Phra Chao Lan Thong'),
  (2,  'MFU Botanical Garden',      NULL),
  (3,  'Rose Garden',               'R2L'),
  (4,  'Culture Plaza',             'Plant trees across the river'),
  (5,  'Pine Grove Plaza',          'Pass the flour overhead'),
  (6,  'Planting Point',            'Tree planting'),
  (7,  'Sub-area 3',                'Reach into the jar'),
  (8,  'Canvas Base',               'Canvas: write your feelings'),
  (9,  'Zero Waste Base',           'Zero Waste & SDGs'),
  (10, 'Finish',                    'End of the route')
) AS v(checkpoint_id, name_en, activity_name_en)
WHERE c.checkpoint_id = v.checkpoint_id;

SELECT setval(pg_get_serial_sequence('checkpoint', 'checkpoint_id'),
              (SELECT MAX(checkpoint_id) FROM checkpoint));
