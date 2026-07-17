-- Adding a NOT NULL column with a VOLATILE default rewrites every row (O(n)) and
-- holds an ACCESS EXCLUSIVE lock for the whole rewrite. PG11+ only fast-paths a
-- NON-volatile default into the catalog; gen_random_uuid() is volatile, so this
-- is a full table rewrite — an outage on a large table.
ALTER TABLE public.orders ADD COLUMN token uuid NOT NULL DEFAULT gen_random_uuid();
