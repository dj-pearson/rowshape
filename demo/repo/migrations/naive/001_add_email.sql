-- Task: "add a NOT NULL email column to users."
--
-- The naive attempt: add the column NOT NULL and give every existing row a
-- placeholder value in one shot. gen_random_uuid() is VOLATILE, so Postgres
-- cannot fast-path the default into the catalog — it rewrites all 5,000,000
-- rows while holding ACCESS EXCLUSIVE. No read or write to users proceeds until
-- it finishes: a write outage.
--
-- rowshape flags this as RS-LOCK-001. It is a WARN (a rewrite is an availability
-- problem, not data corruption), and the demo runs the check with warn-as-fail,
-- so the loop rejects it and the agent rewrites it — see ../rewrite.
ALTER TABLE public.users
  ADD COLUMN email text NOT NULL DEFAULT (gen_random_uuid()::text || '@users.invalid');
