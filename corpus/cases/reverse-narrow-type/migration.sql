-- Narrowing amount from bigint to integer truncates any value that no longer
-- fits. Widening back cannot restore the lost precision: the rollback is lossy
-- (RS-REVERSE-003). (This also rewrites the table under ACCESS EXCLUSIVE, so
-- RS-LOCK fires too — reversibility and lock cost are distinct hazards.)
ALTER TABLE public.ledger ALTER COLUMN amount TYPE integer;
