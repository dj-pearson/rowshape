-- The boundary case: the SAME migration, but here null_fraction is `exact` (a full
-- pass proved 0% NULL). Now the fact CAN license a PASS — capping only downgrades
-- findings resting on estimated/unproven facts, never proven ones (RFC §7.4).
ALTER TABLE public.users ALTER COLUMN email SET NOT NULL;
