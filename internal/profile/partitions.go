package profile

import (
	"context"

	"github.com/rowshape/rowshape/internal/fixture"
)

// measurePartitions describes a partitioned table's shape (RFC §14.2): the
// parent declares count, strategy, and skew, with NO per-partition entries. A
// partitioning migration is reasoned about from this block — partition count and
// per-partition skew change lock behavior materially, and nothing else captures
// it.
func (r *reader) measurePartitions(ctx context.Context, t tableRef) (*fixture.Partitions, error) {
	if t.relkind != "p" {
		return nil, nil // not a partitioned parent
	}

	strategy, err := r.partitionStrategy(ctx, t.oid)
	if err != nil {
		return nil, err
	}
	count, skew, _, err := r.partitionCountSkew(ctx, t.oid)
	if err != nil {
		return nil, err
	}
	return &fixture.Partitions{Count: count, Strategy: strategy, Skew: round6(skew)}, nil
}

// partitionTotalRows returns the summed planner row estimate across a
// partitioned parent's direct partitions. A partitioned parent stores no rows
// itself, so its declared count must come from the partitions (RFC §9).
func (r *reader) partitionTotalRows(ctx context.Context, oid uint32) (int64, error) {
	_, _, sum, err := r.partitionCountSkew(ctx, oid)
	if err != nil {
		return 0, err
	}
	if sum < 0 {
		sum = 0
	}
	return int64(sum), nil
}

// partitionStrategy reads the partitioning strategy (range | list | hash).
func (r *reader) partitionStrategy(ctx context.Context, oid uint32) (string, error) {
	const q = `SELECT partstrat::text FROM pg_partitioned_table WHERE partrelid = $1`
	var s string
	if err := r.tx.QueryRow(ctx, q, oid).Scan(&s); err != nil {
		return "", err
	}
	switch s {
	case "r":
		return "range", nil
	case "l":
		return "list", nil
	case "h":
		return "hash", nil
	default:
		return s, nil
	}
}

// partitionCountSkew returns the number of direct partitions and the fraction of
// rows held by the largest one (1/count is uniform; near 1 means one partition
// dominates). Row counts come from the planner estimate, so skew is estimated.
func (r *reader) partitionCountSkew(ctx context.Context, oid uint32) (count int, skew, sum float64, err error) {
	const q = `
SELECT count(*),
       COALESCE(max(GREATEST(c.reltuples, 0)), 0),
       COALESCE(sum(GREATEST(c.reltuples, 0)), 0)
FROM pg_inherits i
JOIN pg_class c ON c.oid = i.inhrelid
WHERE i.inhparent = $1`
	var maxTuples float64
	if err = r.tx.QueryRow(ctx, q, oid).Scan(&count, &maxTuples, &sum); err != nil {
		return 0, 0, 0, err
	}
	if sum > 0 {
		skew = maxTuples / sum
	}
	return count, skew, sum, nil
}
