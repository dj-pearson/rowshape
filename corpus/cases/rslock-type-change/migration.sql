-- Changing a column's type rewrites the whole table under ACCESS EXCLUSIVE on
-- every Postgres version (no catalog fast-path for an in-place type change).
ALTER TABLE public.events ALTER COLUMN amount TYPE bigint;
