-- The boundary case: the SAME ADD UNIQUE, but here uniqueness is proven `exact`
-- (via an existence probe on a full pass). Now ADD CONSTRAINT UNIQUE can PASS —
-- the migration is safe and the fixture can say so with confidence (§7.4).
ALTER TABLE public.users ADD CONSTRAINT users_email_key UNIQUE (email);
