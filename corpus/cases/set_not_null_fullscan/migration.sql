-- SET NOT NULL full-scans the table to verify no NULLs, holding ACCESS EXCLUSIVE.
-- The fixture shows email is 3.2% NULL in production, so this migration does not
-- just lock — it FAILS: the existing NULL rows violate the new constraint.
ALTER TABLE public.users ALTER COLUMN email SET NOT NULL;
