-- A DELETE on a parent cascades to its children (ON DELETE CASCADE). The fixture
-- shows a long-tailed fan-out (max 12902 orders for one user vs a mean of 8), so
-- deleting the wrong parents can cascade to a huge, slow, lock-holding delete —
-- an outage that a uniform mean would completely hide.
DELETE FROM public.users WHERE last_seen_at < now() - interval '2 years';
