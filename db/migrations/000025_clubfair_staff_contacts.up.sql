-- ============================================================
-- Club Fair — who staff call, and about what.
--
-- The website has a staff surface now (`/[lang]/staff`), and its whole content
-- is the floor plan and this list. The plan comes from `booth` and
-- `clubfair_zone` and looks after itself; the contacts started life as a
-- constant in the website's `lib/contacts.ts`, which is the arrangement this
-- schema keeps deciding against — for the same reason as the fair's dates in
-- 000023 and the prize thresholds in 000022:
--
--   **A rota changes on the morning of the fair.** Somebody swaps a shift, a
--   number turns out to be wrong, first aid moves to a different phone. Every
--   one of those is a redeploy if the list is in client code, and the fair is
--   one weekend — a redeploy mid-event is not a plan.
--
-- ⚠ **Not public, unlike every other Club Fair read.** `/booths`, `/zones`,
-- `/info`, `/program` and `/prizes` are open on purpose: a student deciding
-- whether to come should not have to sign in. This is the opposite. It is a list
-- of named people's phone numbers, collected so a volunteer can reach the prize
-- desk — not so two thousand students can. The read is gated with
-- `requireClubFairStaff`.
-- ============================================================

CREATE TABLE clubfair_staff_contact (
    id SERIAL PRIMARY KEY,

    -- What this person is called *about*, which is the column that makes the
    -- list usable: a staff member scanning it is looking for "first aid", not
    -- for a name they have never heard. Thai is the original and English the
    -- translation, NULL falling back to Thai — the convention 000007 set for
    -- checkpoint.name_en and 000019 kept for booth.name_en.
    role    TEXT NOT NULL CHECK (length(btrim(role)) > 0),
    role_en TEXT,

    -- Who, and how to reach them. **Both nullable, and that is the point.**
    -- The roles are knowable before the rota is; a row with a role and no
    -- number renders as "not filled in yet" and tells a volunteer that first aid
    -- *has* an owner this screen does not know. An invented number would not
    -- announce itself as invented — it would simply not answer, at the moment
    -- somebody needed it to.
    name  TEXT,
    -- Text, not a numeric type. A phone number is a string of digits that may
    -- carry a leading zero, an extension, or be an internal four-digit number;
    -- it is never arithmetic. No format CHECK either — this has to hold "ext.
    -- 3021" and "080-123-4567" and whatever the SU actually writes down.
    phone TEXT,

    -- One line of "call this one when…", for the roles where the title alone is
    -- ambiguous. Nullable: "first aid" needs no gloss.
    note    TEXT,
    note_en TEXT,

    -- The order staff read them in, which is not alphabetical and not the order
    -- they were typed. The prize desk and the booth screens are what get called;
    -- lost property is not. Ties break on id — see the repository's ORDER BY.
    sort_order INTEGER NOT NULL DEFAULT 0,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Who last edited it. SET NULL rather than CASCADE, as elsewhere: losing a
    -- staff account must not delete the rota.
    updated_by INTEGER REFERENCES clubfair_users(id) ON DELETE SET NULL
);

-- ------------------------------------------------------------
-- Seeded with the roles and **no people**.
--
-- This is the same line 000023 drew when it seeded the fair's dates but not a
-- programme: seed what the system already knows, never what it would have to
-- invent. The five roles below are what a fair has to deal with and are safe to
-- assert; the names and numbers behind them are facts about the Student Union's
-- rota that exist nowhere this migration can read, so they are left NULL for the
-- dashboard to fill in.
--
-- Editing or deleting any of these rows is expected — they are a starting shape,
-- not a fixed vocabulary.
-- ------------------------------------------------------------

INSERT INTO clubfair_staff_contact (role, role_en, note, note_en, sort_order) VALUES
    ('โต๊ะรับของรางวัล', 'Prize desk',
     'รหัสรางวัลในแอปใช้ไม่ได้ หรือมีคำถามเรื่องแต้ม MFU333',
     'A prize code in the app will not go through, or a question about the MFU333 point.',
     10),

    -- The failure this one covers is the check-in scheme's: a display that has
    -- stopped polling shows a code that no longer scans, and the booth's
    -- volunteers cannot tell that apart from a student's phone being at fault.
    ('หน้าจอบูธและการสแกน', 'Booth screens and scanning',
     'บูธไหน QR สแกนไม่ได้ หรือจอค้างอยู่ที่รหัสเดิม',
     'A booth whose QR will not scan, or a screen stuck on one code.',
     20),

    ('ประกาศถึงผู้เข้าร่วม', 'Announcements',
     'ต้องประกาศถึงนักศึกษาทุกคนในแอป',
     'Something has to go out to every student in the app.',
     30),

    ('ปฐมพยาบาล', 'First aid', NULL, NULL, 40),
    ('ของหาย',    'Lost property', NULL, NULL, 50);
