-- ============================================================
-- Club Fair — the actual floor plan.
--
-- From แผนผังภายในบริเวณพื้นที่กิจกรรม (เสนอ). This replaces two guesses made in
-- 000015, and both were wrong in the same direction — assuming the mobile app's
-- three "themed habitats" were a mockup with nothing behind them:
--
--  1. They are the real zones. Rainforest / Savannah / Deep Ocean are the
--     Student Union's own names for the three areas of the floor, and they are
--     signed that way. 000015 left `zone` as unconstrained free text on the
--     reasoning that a CHECK would bake a mockup into the schema; there is a
--     `clubfair_zone` table below instead, because the names are real data and
--     they are needed in two languages.
--
--  2. A booth's printed code is `A1`, `B16`, `C5` — alphanumeric. 000015 added
--     `display_number INT`, which cannot hold any of them. Dropped here; it was
--     never populated, so nothing is lost.
--
-- The zones turn out to partition the existing `category` values exactly, which
-- is why the count works out: religion_and_culture + volunteer = 7 = A1–A7,
-- sports + student_relations = 16 = B1–B16, academic = 5 = C1–C5. Both columns
-- are kept. Zone is where a booth physically stands and is what the app
-- navigates by; category is what kind of club it is. They agree today and there
-- is no guarantee next year's layout keeps them aligned, so neither is derived
-- from the other.
--
-- ⚠ Not represented here, deliberately: E1–E3 (สวัสดิการ Service, SC, SU) and
-- F1–F4 (Sponsor Zone). They are on the plan and they are not clubs — nothing
-- to stamp, and `booth.category` has no value that would fit them. If the app
-- ever draws a map of the floor they will need a table of their own rather than
-- rows in `booth` that a student could collect.
-- ============================================================

-- ------------------------------------------------------------
-- The three areas.
--
-- `name` is Thai and `name_en` is the translation, following 000007's
-- convention: the Thai column is the original and NULL English falls back to it.
-- ------------------------------------------------------------

CREATE TABLE clubfair_zone (
    -- The letter on the signage, and the prefix of every booth code in the area.
    code       CHAR(1) PRIMARY KEY,
    name       TEXT NOT NULL,
    name_en    TEXT,
    -- What kind of club the area is for, in the SU's own words. Not a
    -- constraint on anything — the mapping to `booth.category` is documented in
    -- the header, not enforced.
    intent     TEXT,
    -- Display order: Rainforest, Savannah, Deep Ocean. Not alphabetical by
    -- accident — A/B/C happens to sort correctly, and relying on that would
    -- break the first time a zone D is inserted between two of them.
    sort_order INT NOT NULL
);

INSERT INTO clubfair_zone (code, name, name_en, intent, sort_order) VALUES
    ('A', 'โซนป่าดิบชื้น',      'Rainforest Zone',  'ชมรมด้านศาสนาและวัฒนธรรม และชมรมด้านการบำเพ็ญประโยชน์', 1),
    ('B', 'โซนทุ่งหญ้าสะวันนา', 'Savannah Zone',    'ชมรมด้านกีฬาและชมรมด้านนักศึกษาสัมพันธ์',                2),
    ('C', 'โซนมหาสมุทรลึก',    'Deep Ocean Zone',  'ชมรมด้านวิชาการ',                                       3);

-- ------------------------------------------------------------
-- Booth code, replacing 000015's integer.
-- ------------------------------------------------------------

DROP INDEX IF EXISTS idx_booth_zone;
ALTER TABLE booth DROP COLUMN IF EXISTS display_number;

-- UNIQUE: two stalls cannot carry the same sign. Nullable so a booth can be
-- created before the floor is laid out.
ALTER TABLE booth ADD COLUMN IF NOT EXISTS booth_code TEXT UNIQUE;

-- English club name, same convention as checkpoint.name_en in 000007: `name`
-- holds the Thai original, this is the translation, NULL falls back to Thai.
ALTER TABLE booth ADD COLUMN IF NOT EXISTS name_en TEXT;

-- ------------------------------------------------------------
-- Assign every booth its zone, code and English name.
--
-- Matched on `id`, never on `name`. The names are Thai and several differ
-- between the plan and the 000010 seed by an abbreviation — the plan's
-- "ชมรมสัตว์ป่า" is the seed's "ชมรมอนุรักษ์พันธุ์สัตว์ป่าและป่าไม้", "ชมรมคฑากร" is
-- "ชมรมแม่ฟ้าคฑากร", "MFU HRC" is "MFU Human Rights Club" — so matching on text
-- would silently miss rows and leave them unzoned.
--
-- ⚠ The English names need a native check before release, like the app's Thai
-- strings. The plumbing is right; the wording has not been read by anyone.
-- ------------------------------------------------------------

UPDATE booth AS b SET
    zone       = v.zone,
    booth_code = v.booth_code,
    name_en    = v.name_en
FROM (VALUES
    -- Zone A — Rainforest: religion & culture, and volunteer.
    (17, 'A', 'A1',  'Wildlife & Forest Conservation Club'),
    (18, 'A', 'A2',  'Community Development Volunteer Club'),
    (19, 'A', 'A3',  'Highland Teachers Club'),
    (20, 'A', 'A4',  'Buddhism Club'),
    (22, 'A', 'A5',  'Christian Club'),
    (23, 'A', 'A6',  'Thai & Folk Music Club'),
    (21, 'A', 'A7',  'Thai Classical Dance Club'),

    -- Zone B — Savannah: sports and student relations. B1–B8 are the back row
    -- of the block and B16–B9 the front, which is why the codes run out and
    -- back rather than left to right.
    (16, 'B', 'B1',  'DAC Club'),
    (13, 'B', 'B2',  'Cheerleading Club'),
    (15, 'B', 'B3',  'Drum Major Club'),
    (14, 'B', 'B4',  'Student Welfare Club'),
    (10, 'B', 'B5',  'Brazilian Jiu-Jitsu Club'),
    (3,  'B', 'B6',  'Kendo Club'),
    (5,  'B', 'B7',  'Muay Thai Club'),
    (2,  'B', 'B8',  'Taekwondo Club'),
    (9,  'B', 'B9',  'Board Games Club'),
    (11, 'B', 'B10', 'Football Club'),
    (12, 'B', 'B11', 'Basketball Club'),
    (8,  'B', 'B12', 'Rugby Club'),
    (6,  'B', 'B13', 'Volleyball Club'),
    (7,  'B', 'B14', 'Badminton Club'),
    (1,  'B', 'B15', 'Petanque Club'),
    (4,  'B', 'B16', 'Swimming Club'),

    -- Zone C — Deep Ocean: academic.
    (24, 'C', 'C1',  'Drama Club'),
    (25, 'C', 'C2',  'Master of Ceremonies Club'),
    (26, 'C', 'C3',  'MFU Model United Nations'),
    (28, 'C', 'C4',  'MFU Human Rights Club'),
    (27, 'C', 'C5',  'MFU Photo Club')
) AS v(id, zone, booth_code, name_en)
WHERE b.id = v.id;

-- ------------------------------------------------------------
-- Now that every booth has one, make the zone a real reference.
-- ------------------------------------------------------------

ALTER TABLE booth
    ADD CONSTRAINT booth_zone_fkey
    FOREIGN KEY (zone) REFERENCES clubfair_zone(code);

-- The app's most frequent booth query is "the wall for this zone, in code order".
CREATE INDEX idx_booth_zone_code ON booth (zone, booth_code);

-- ------------------------------------------------------------
-- Refuse to finish if the plan and the seed disagree.
--
-- A migration that silently leaves four booths unzoned produces a Booths tab
-- with four clubs missing, discovered by a student at the fair rather than here.
-- ------------------------------------------------------------

DO $$
DECLARE
    unzoned INT;
    per_zone TEXT;
BEGIN
    SELECT count(*) INTO unzoned
      FROM booth WHERE zone IS NULL OR booth_code IS NULL;

    IF unzoned > 0 THEN
        RAISE EXCEPTION
            '000016: % booth(s) left without a zone or code — the floor plan and the 000010 seed disagree',
            unzoned;
    END IF;

    SELECT string_agg(z.code || '=' || c, ', ' ORDER BY z.code)
      INTO per_zone
      FROM clubfair_zone z
      JOIN (SELECT zone, count(*) c FROM booth GROUP BY zone) t ON t.zone = z.code;

    -- 7 / 16 / 5 from the plan.
    IF per_zone IS DISTINCT FROM 'A=7, B=16, C=5' THEN
        RAISE EXCEPTION '000016: zone counts are %, expected A=7, B=16, C=5', per_zone;
    END IF;
END $$;
