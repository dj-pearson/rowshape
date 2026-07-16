-- Validating a foreign key scans every child row for a matching parent. The
-- fixture records orphan_fraction > 0 (exact, via scan): rows already violate the
-- FK — added earlier as NOT VALID and never cleaned up — so VALIDATE FAILS.
ALTER TABLE public.orders
  ADD CONSTRAINT orders_user_fk FOREIGN KEY (user_id) REFERENCES public.users (id) NOT VALID;
ALTER TABLE public.orders VALIDATE CONSTRAINT orders_user_fk;
