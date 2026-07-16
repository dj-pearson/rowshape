-- Dropping a column permanently loses its values across all 4.2M rows. A
-- down-migration can recreate legacy_notes but not what it held: the rollback is
-- lossy (RS-REVERSE-001).
ALTER TABLE public.users DROP COLUMN legacy_notes;
