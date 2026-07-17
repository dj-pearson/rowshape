-- Step 2 of 3: backfill the new column.
--
-- The WHERE clause keeps this from being an unqualified whole-table write
-- (RS-PERF-002). In production this runs in bounded batches (e.g. by id range)
-- so it never holds a long lock or bloats the table in one transaction; the
-- demo's disposable database is small enough to do it in one statement.
UPDATE public.users
   SET email = 'user_' || id || '@users.invalid'
 WHERE email IS NULL;
