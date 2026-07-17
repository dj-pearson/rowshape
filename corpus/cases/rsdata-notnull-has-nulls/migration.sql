-- The column already contains NULLs (null_fraction 2%, exact via scan). SET NOT
-- NULL scans the table and rejects those rows: the migration WILL fail.
ALTER TABLE public.users ALTER COLUMN email SET NOT NULL;
