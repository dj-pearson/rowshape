-- ADD CONSTRAINT NOT VALID then VALIDATE in ONE transaction: the validating scan
-- still runs under the transaction's locks, defeating the split whose entire
-- purpose is to avoid holding a lock during the scan.
BEGIN;
ALTER TABLE public.orders ADD CONSTRAINT orders_total_positive CHECK (total_cents > 0) NOT VALID;
ALTER TABLE public.orders VALIDATE CONSTRAINT orders_total_positive;
COMMIT;
