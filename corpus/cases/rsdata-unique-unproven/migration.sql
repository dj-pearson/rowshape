-- ADD CONSTRAINT UNIQUE with no proven `unique` fact. distinct is only estimated,
-- and a sample never establishes uniqueness (INV-UNIQUENESS): cannot PASS.
ALTER TABLE public.users ADD CONSTRAINT users_email_key UNIQUE (email);
