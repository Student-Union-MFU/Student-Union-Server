-- ชื่อฐาน/กิจกรรมภาษาอังกฤษ — name/activity_name เดิมถือเป็นภาษาไทย
-- ค่า NULL = ยังไม่มีคำแปล → ฝั่ง frontend fall back ไปแสดงภาษาไทย
ALTER TABLE checkpoint ADD COLUMN IF NOT EXISTS name_en          TEXT;
ALTER TABLE checkpoint ADD COLUMN IF NOT EXISTS activity_name_en TEXT;

-- เติมคำแปลให้แถวที่ seed มาแล้ว (000006)
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
