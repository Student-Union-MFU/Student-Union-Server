-- ============================================================
-- 000015_wbw_sos — ทำให้ sos_event ที่มีมาตั้งแต่ 000005 ใช้งานได้จริง
--
-- ตารางเดิมมีแค่ "ใครกด ที่ไหน เมื่อไหร่ จบหรือยัง" ซึ่งไม่พอสำหรับการส่งคน
-- ไปช่วย: ไม่รู้ว่าเจ็บเองหรือแจ้งแทน ไม่รู้ว่าควรถึงมือฐานไหน ไม่รู้ว่ามีใคร
-- รับเรื่องแล้วหรือยัง
--
-- resolved ยังเป็นธงจบเหมือนเดิมโดยตั้งใจ — wbw_admin_repository.go:78 นับ
-- open_sos ด้วย resolved = FALSE การเพิ่มคอลัมน์สถานะแยกจะทำให้ตัวเลขนั้นผิด
-- เงียบๆ · ยกเลิกเองจึงเป็น resolved = TRUE พร้อม reason = canceled_by_user
-- ============================================================

ALTER TABLE sos_event
  ADD COLUMN IF NOT EXISTS for_other      BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS group_id       INT  REFERENCES participant_group(group_id),
  ADD COLUMN IF NOT EXISTS checkpoint_id  INT  REFERENCES checkpoint(checkpoint_id),
  ADD COLUMN IF NOT EXISTS accuracy_m     DOUBLE PRECISION,
  ADD COLUMN IF NOT EXISTS loc_source     TEXT,
  ADD COLUMN IF NOT EXISTS acked_at       TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS acked_by       UUID REFERENCES app_user(user_id),
  ADD COLUMN IF NOT EXISTS resolve_reason TEXT,
  ADD COLUMN IF NOT EXISTS last_push_at   TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS updated_at     TIMESTAMPTZ NOT NULL DEFAULT now();

-- คนหนึ่งเปิดเคสได้ทีละหนึ่ง — นี่คือตัวกันกดรัว ไม่ใช่แค่ constraint สวยงาม
-- กดซ้ำระหว่างเคสเปิดอยู่กลายเป็น "ย้ำ" (อัปเดตแถวเดิม) ไม่ใช่เคสใหม่
CREATE UNIQUE INDEX IF NOT EXISTS sos_one_open_per_user
  ON sos_event (participant_id) WHERE NOT resolved;

CREATE INDEX IF NOT EXISTS sos_feed_idx ON sos_event (updated_at DESC);
