-- SET NOT NULL is safe only if the column is truly 0% NULL. Here null_fraction is
-- `estimated` (from the planner's sample), so the fixture CANNOT confirm it — a
-- confident PASS would be a wrong PASS. Capping (RFC §7.4) forces WARN and names
-- the command that resolves it.
ALTER TABLE public.users ALTER COLUMN email SET NOT NULL;
