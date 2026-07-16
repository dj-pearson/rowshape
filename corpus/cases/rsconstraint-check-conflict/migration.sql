-- Adding CHECK (balance_cents >= 0) against a column whose profiled range dips to
-- -4500: existing rows already violate the predicate, so validating the CHECK
-- fails. The data shape in the fixture predicts it before you run the migration.
ALTER TABLE public.accounts ADD CONSTRAINT accounts_balance_nonneg CHECK (balance_cents >= 0);
