-- An UPDATE with no WHERE clause rewrites every row of a 6M-row table: a slow,
-- bloat-inducing, lock-holding full scan that is almost never what was intended
-- (RS-PERF-002).
UPDATE public.accounts SET status = 'active';
