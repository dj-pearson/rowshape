-- Building a UNIQUE index requires the column to actually be unique. The fixture
-- proves (exact, via scan) that email has duplicates, so the index build FAILS.
CREATE UNIQUE INDEX CONCURRENTLY users_email_uniq ON public.users (email);
