-- Adding a constraint NOT VALID (cheap, catalog-only) and then VALIDATE-ing it in
-- the SAME transaction throws away the entire benefit: the transaction still runs
-- the full validating scan while holding its locks, so the two-step split that
-- would have avoided a long lock is defeated.
BEGIN;
ALTER TABLE public.orders ADD CONSTRAINT orders_amount_positive CHECK (amount_cents > 0) NOT VALID;
ALTER TABLE public.orders VALIDATE CONSTRAINT orders_amount_positive;
COMMIT;
