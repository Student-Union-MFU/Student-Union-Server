-- ============================================================
-- Undo 000027 — drop pg_stat_statements.
--
-- Drops the view and its functions. Everything it had collected goes with them,
-- which costs nothing that is not already lost on a restart: the counters live
-- in shared memory and start again from zero each time Postgres does.
--
-- The `shared_preload_libraries` entry in docker-compose.yml is NOT undone here
-- and does not need to be. An entry naming a library whose extension is not
-- installed is harmless — the library loads, tracks statements into shared
-- memory, and nothing has a view to read them through.
-- ============================================================

DROP EXTENSION IF EXISTS pg_stat_statements;
