-- Reverses 000024.
--
-- ⚠ Order matters and so does the demotion. The role CHECK cannot go back to
-- three values while any row still says 'booth_owner' — the constraint is
-- validated against existing data when it is added, so this would fail with a
-- 23514 naming a constraint the operator did not just write.
--
-- Demoted to 'student' rather than 'staff'. Down-migrating is not the moment to
-- hand anyone the announcements channel and the participant roster, and student
-- is the role every one of these accounts had before it was assigned.

DROP TABLE IF EXISTS clubfair_booth_owner;

UPDATE clubfair_users SET role = 'student' WHERE role = 'booth_owner';

ALTER TABLE clubfair_users DROP CONSTRAINT clubfair_users_role_check;

ALTER TABLE clubfair_users ADD CONSTRAINT clubfair_users_role_check
    CHECK (role IN ('student', 'staff', 'admin'));
