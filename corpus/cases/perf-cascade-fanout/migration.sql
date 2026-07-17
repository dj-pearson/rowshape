-- DELETE on a parent with an ON DELETE CASCADE child whose fan-out is long-tailed
-- (max 12902 vs mean 8). Deleting the wrong parents cascades to a huge, slow,
-- lock-holding delete — the tail an average completely hides (RS-PERF-001).
DELETE FROM public.customers WHERE last_seen_at < now() - interval '3 years';
