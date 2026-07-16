-- Dropping a table permanently removes every one of its 88M rows. A
-- down-migration can recreate audit_log's structure but not its data: the
-- rollback cannot restore what was there (RS-REVERSE-002).
DROP TABLE public.audit_log;
