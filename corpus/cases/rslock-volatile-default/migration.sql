-- Adding a NOT NULL column with a VOLATILE default (gen_random_uuid) rewrites
-- every row under ACCESS EXCLUSIVE. PG 11+ only fast-paths a NON-volatile
-- default into the catalog; a volatile one rewrites on every version.
ALTER TABLE public.orders ADD COLUMN token uuid NOT NULL DEFAULT gen_random_uuid();
