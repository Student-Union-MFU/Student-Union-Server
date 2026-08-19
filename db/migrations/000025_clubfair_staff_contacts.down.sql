-- Reverse of 000025.
--
-- Nothing references this table — its only outbound FK is `updated_by` to
-- clubfair_users, and nothing joins to it — so the drop is unconditional.
--
-- ⚠ Rolling this back takes the rota with it. The website's `lib/contacts.ts`
-- keeps the roles as a fallback for a server that has not had this applied yet,
-- the way `lib/fair.ts` does for the dates — but the fallback carries no names
-- and no numbers, because those only ever existed in these rows.

DROP TABLE IF EXISTS clubfair_staff_contact;
