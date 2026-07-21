-- ล้างข้อมูลอ้างอิง WBW (ตารางยังอยู่ — โครงสร้างอยู่ที่ 000005)
DELETE FROM checkpoint        WHERE checkpoint_id <= 12;
DELETE FROM participant_group WHERE group_id      <= 40;
DELETE FROM school            WHERE school_id     <= 14;
