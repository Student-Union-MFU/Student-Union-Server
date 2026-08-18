-- Reverse of 000023.
--
-- Both tables go entirely. Nothing else references them — the programme's only
-- outbound FK is to `clubfair_zone`, and `clubfair_fair_info` is read by
-- clients rather than joined to — so the drops are unordered and safe.
--
-- ⚠ Rolling this back does not restore the dates to anywhere. The Android app
-- and the website read them from here once this is applied; going back means
-- both are reading an endpoint that has stopped existing, and the fair's window
-- lives only in whatever fallback they kept.

DROP INDEX IF EXISTS idx_clubfair_program_running_order;
DROP TABLE IF EXISTS clubfair_program;
DROP TABLE IF EXISTS clubfair_fair_info;
