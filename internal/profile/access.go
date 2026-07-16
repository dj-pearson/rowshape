package profile

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrSuperuser is returned when the connected role is a superuser and the
// caller has not passed the explicit override. A conformant emitter MUST refuse
// to run as superuser absent explicit override (RFC §13, INV-BLAST-RADIUS-ZERO):
// a superuser can bypass the read-only guarantees the format promises.
var ErrSuperuser = fmt.Errorf("refusing to profile as a superuser: connect with a read-only role, or pass --i-know to override")

// CheckAccess enforces the read-only-role posture. When the connected role is a
// superuser it refuses unless allowSuperuser is set.
func CheckAccess(ctx context.Context, conn *pgx.Conn, allowSuperuser bool) error {
	var isSuper bool
	const q = `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`
	if err := conn.QueryRow(ctx, q).Scan(&isSuper); err != nil {
		return fmt.Errorf("check role privileges: %w", err)
	}
	if isSuper && !allowSuperuser {
		return ErrSuperuser
	}
	return nil
}
