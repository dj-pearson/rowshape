-- Uniqueness of email is NOT recorded in the fixture (no `unique` fact at all).
-- A unique index can only PASS if uniqueness is PROVEN exact — a sample never
-- establishes it (INV-UNIQUENESS). So this must not certify: capping turns the
-- would-be PASS into a WARN that names the command which resolves it (RFC §7.4).
CREATE UNIQUE INDEX users_email_key ON public.users (email);
