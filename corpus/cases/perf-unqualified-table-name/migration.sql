-- The table name is written WITHOUT its schema, which is how ordinary
-- hand-written SQL looks — Postgres resolves `accounts` via search_path. RFC §5
-- keys fixture tables by QUALIFIED name, so the analyzer must map `accounts`
-- onto `public.accounts` before looking it up. Before CR-T2 it did not, the
-- lookup missed, and RS-PERF-002 was silently dropped for a 6M-row rewrite.
--
-- Note this is a DIFFERENT sense of "unqualified" from perf-unqualified-update,
-- which is about the missing WHERE clause. This case is about the table NAME.
UPDATE accounts SET status = 'active';
