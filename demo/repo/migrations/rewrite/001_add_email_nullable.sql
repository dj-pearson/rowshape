-- Step 1 of 3: add the column NULLABLE with no default.
--
-- Adding a nullable column with no default is a catalog-only change on modern
-- Postgres — no table rewrite, no long lock. This is the "expand" step.
ALTER TABLE public.users ADD COLUMN email text;
