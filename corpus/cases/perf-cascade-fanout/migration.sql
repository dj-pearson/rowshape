-- TRUNCATE on a parent referenced ON DELETE CASCADE. Same long-tailed fan-out
-- hazard as a bulk DELETE (RS-PERF-001), reached through a DIFFERENT parser
-- branch: deleteTarget handles `TRUNCATE [TABLE] <name>` separately from
-- `DELETE FROM <name>`.
--
-- CR-T15: this case previously duplicated cascade_delete_fanout almost exactly
-- (a bulk DELETE differing only in table name and interval), while the TRUNCATE
-- branch had no corpus coverage at all. Differentiated rather than deleted, so
-- the case now buys coverage instead of repeating it.
TRUNCATE TABLE public.customers CASCADE;
