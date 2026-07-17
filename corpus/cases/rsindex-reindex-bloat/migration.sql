-- A non-concurrent REINDEX rebuilds the whole index under a lock that blocks
-- writes; its cost is driven by the index's on-disk size and bloat.
REINDEX INDEX orders_customer_idx;
