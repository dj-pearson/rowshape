-- A plain CREATE INDEX holds a lock that blocks writes for the whole build.
-- On a large table that is a long write outage; CREATE INDEX CONCURRENTLY
-- builds without the exclusive lock.
CREATE INDEX orders_created_at_idx ON public.orders (created_at);
