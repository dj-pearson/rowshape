package plan

import "testing"

// TestRedactURL: a connection URL must never carry its credentials into anything
// rowshape shows, echoes, or returns (PRD §5 — the connection URL and any
// credentials are never logged, persisted, or written into a fixture).
//
// This lands here because it was NOT covered anywhere: the only test that looked
// like it checked redaction guarded on the URL containing "@" and ran against a
// localhost URL, so the assertion could never fire. A vacuous check on the buyer's
// stated requirement is worse than no check — it reads as covered in review.
func TestRedactURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "user and password are stripped",
			in:   "postgres://admin:hunter2@db.internal:5432/app",
			want: "postgres://…@db.internal:5432/app",
		},
		{
			name: "user without a password is still stripped",
			in:   "postgres://admin@db.internal:5432/app",
			want: "postgres://…@db.internal:5432/app",
		},
		{
			name: "a password containing @ does not leak the earlier segment",
			in:   "postgres://admin:p@ss@db.internal:5432/app",
			want: "postgres://…@db.internal:5432/app",
		},
		{
			name: "no credentials, unchanged",
			in:   "postgres://localhost:5432/app",
			want: "postgres://localhost:5432/app",
		},
		{
			// An "@" outside the authority is not a credential separator; treating
			// it as one would corrupt the URL this only means to display.
			name: "an @ in the query string is not mistaken for credentials",
			in:   "postgres://localhost:5432/app?options=user@host",
			want: "postgres://localhost:5432/app?options=user@host",
		},
		{
			name: "ipv6 host",
			in:   "postgres://admin:hunter2@[::1]:5432/app",
			want: "postgres://…@[::1]:5432/app",
		},
		{
			name: "no scheme, unchanged",
			in:   "just-a-string@thing",
			want: "just-a-string@thing",
		},
		{
			name: "empty string",
			in:   "",
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RedactURL(c.in)
			if got != c.want {
				t.Errorf("RedactURL(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestRedactURLLeaksNoSecret is the property that actually matters: whatever the
// shape of the URL, the secret must not survive redaction. Table cases assert the
// format; this asserts the guarantee.
func TestRedactURLLeaksNoSecret(t *testing.T) {
	const secret = "hunter2"
	urls := []string{
		"postgres://admin:" + secret + "@db.internal:5432/app",
		"postgresql://u:" + secret + "@10.0.0.1/db?sslmode=require",
		"postgres://admin:" + secret + "@[::1]:5432/app",
	}
	for _, u := range urls {
		if got := RedactURL(u); contains(got, secret) {
			t.Errorf("RedactURL(%q) = %q — the password survived redaction", u, got)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
