-- The CORRECT two-step pattern: add the constraint NOT VALID in one
-- transaction, then VALIDATE it in a SEPARATE one. The whole point of the split
-- is that the validating scan runs without holding the first transaction's
-- locks, so this must NOT be flagged.
--
-- CR-T15: this case previously duplicated not_valid_validated_same_tx almost
-- exactly (both wrapped in one BEGIN/COMMIT, differing only in constraint and
-- column name). Differentiated into the NEGATIVE case instead: nothing verified
-- that RS-CONSTRAINT-001 stays silent on the pattern it tells people to use, and
-- a finding that fires on its own remediation is how a check gets switched off.
BEGIN;
ALTER TABLE public.orders ADD CONSTRAINT orders_total_positive CHECK (total_cents > 0) NOT VALID;
COMMIT;

BEGIN;
ALTER TABLE public.orders VALIDATE CONSTRAINT orders_total_positive;
COMMIT;
