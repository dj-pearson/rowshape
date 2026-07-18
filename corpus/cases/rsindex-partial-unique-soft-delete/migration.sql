-- The soft-delete pattern: email is NOT unique across the whole table (the
-- duplicates are old, soft-deleted rows), but it must be unique among the LIVE
-- rows. A partial unique index expresses exactly that, and it builds fine.
--
-- The fixture records unique=false (exact) for the column as a whole. Judging
-- this index by that fact produces a confident, WRONG FAIL: the duplicates live
-- entirely in rows the predicate excludes. rowshape must decline to decide
-- rather than guess in either direction (CR-T5, INV-UNIQUENESS).
CREATE UNIQUE INDEX users_live_email_idx ON public.users (email) WHERE deleted_at IS NULL;
