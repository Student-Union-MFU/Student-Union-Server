-- ============================================================
-- Booth icons.
--
-- A neutral token — 'football', 'photo' — not an Android drawable name and not
-- a URL. Each client maps it to its own asset, which is why it lives here rather
-- than as 28 hardcoded cases in the app: an icon can be corrected without a Play
-- Store release, and the iOS client gets the same mapping for free.
--
-- No CHECK. A token the client does not recognise has to fall back to a neutral
-- glyph anyway — that is the same code path as NULL — so constraining the column
-- would only convert a soft, already-handled case into a failed migration.
--
-- ⚠ SIX booths are deliberately NULL: nothing in the existing icon set is close
-- enough, and a wrong glyph on a club's card is worse than none. They are listed
-- at the bottom of this file and need real art:
--   A4 Buddhism, A5 Christian, B1 DAC, B9 Board Games,
--   B15 Petanque, C4 Human Rights
--
-- ⚠ FIVE more share a glyph with another booth, marked `dup` below. All are
-- semantically fair — four martial arts really are one category, rugby and
-- football really are two football codes — but a zone showing the same glyph
-- four times reads as a bug rather than as a grouping. Also wants art.
-- ============================================================

ALTER TABLE booth ADD COLUMN IF NOT EXISTS icon TEXT;

UPDATE booth AS b SET icon = v.icon
FROM (VALUES
    -- Zone A — Rainforest
    ('A1',  'environment'),
    ('A2',  'volunteer'),
    ('A3',  'school'),        -- Highland Teachers: teaching, not a club sport
    -- A4 Buddhism   — no glyph
    -- A5 Christian  — no glyph
    ('A6',  'music'),
    ('A7',  'thaiarts'),      -- นาฏศิลป์: Thai classical, not generic dance

    -- Zone B — Savannah
    -- B1 DAC — no glyph, and the club's activity is not evident from the name
    ('B2',  'dance'),         -- cheerleading
    ('B3',  'music'),         -- dup A6: drum major is a marching band role
    ('B4',  'members'),       -- student welfare
    ('B5',  'muaythai'),      -- dup B7: martial arts
    ('B6',  'muaythai'),      -- dup B7: martial arts
    ('B7',  'muaythai'),
    ('B8',  'muaythai'),      -- dup B7: martial arts
    -- B9 Board Games — no glyph
    ('B10', 'football'),
    ('B11', 'basketball'),
    ('B12', 'football'),      -- dup B10: rugby is the other football code
    ('B13', 'volleyball'),
    ('B14', 'badminton'),
    -- B15 Petanque — no glyph
    ('B16', 'swimming'),

    -- Zone C — Deep Ocean
    ('C1',  'drama'),
    ('C2',  'debate'),        -- พิธีกร: public speaking
    ('C3',  'international'), -- Model UN
    -- C4 Human Rights — no glyph
    ('C5',  'photo')
) AS v(booth_code, icon)
WHERE b.booth_code = v.booth_code;

-- Leaves exactly the six above without one. Asserted rather than trusted: a typo
-- in a booth_code up there would silently skip a row, and the first anyone would
-- know is a blank card at the fair.
DO $$
DECLARE
    missing TEXT;
BEGIN
    SELECT string_agg(booth_code, ', ' ORDER BY booth_code)
      INTO missing
      FROM booth WHERE icon IS NULL;

    IF missing IS DISTINCT FROM 'A4, A5, B1, B15, B9, C4' THEN
        RAISE EXCEPTION
            '000017: booths without an icon are (%), expected exactly A4, A5, B1, B15, B9, C4',
            missing;
    END IF;
END $$;
