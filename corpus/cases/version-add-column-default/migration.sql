-- ADD COLUMN with a CONSTANT (non-volatile) default. This is the operation whose
-- cost is engine-version-conditional (RFC §9.1): PG 11+ fast-paths the constant
-- into the catalog (O(1), instant) instead of rewriting, while PG 10 and earlier
-- rewrite every one of the 30M rows under ACCESS EXCLUSIVE — a write outage.
-- The SAME migration is a PASS on 11+ and a RS-LOCK WARN on 10 (see D-006/D-007).
ALTER TABLE public.customers ADD COLUMN region text NOT NULL DEFAULT 'us';
