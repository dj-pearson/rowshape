-- ADD CONSTRAINT UNIQUE is safe only if the column is actually unique. Here the
-- fixture has NO uniqueness proof (unique is absent; distinct is only estimated),
-- so a sample cannot establish uniqueness (§7.2). A PASS here would certify a
-- migration that may fail in production — the one outcome that kills the project.
-- Capping forces WARN and names the resolving command.
ALTER TABLE public.users ADD CONSTRAINT users_email_key UNIQUE (email);
