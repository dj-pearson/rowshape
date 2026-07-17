package fixture

import "testing"

func tablesFixture(keys ...string) *Fixture {
	f := &Fixture{Tables: map[string]Table{}}
	for _, k := range keys {
		f.Tables[k] = Table{Rows: Fact[int64]{Value: 50_000_000}}
	}
	return f
}

// TestResolveTable: RFC §5 keys tables by qualified name; migrations say
// `ALTER TABLE users`. Bridging the two is what keeps an unqualified statement
// from reading the zero value and reporting a 50M-row rewrite as `instant`.
func TestResolveTable(t *testing.T) {
	cases := []struct {
		name   string
		tables []string
		lookup string
		want   string
		wantOK bool
	}{
		{
			name:   "unqualified name resolves to the one matching table",
			tables: []string{"public.users", "public.orders"},
			lookup: "users", want: "public.users", wantOK: true,
		},
		{
			name:   "exact qualified match wins",
			tables: []string{"public.users", "tenant.users"},
			lookup: "public.users", want: "public.users", wantOK: true,
		},
		{
			// Which one `users` means depends on the search_path, which the fixture
			// does not record. Answering from the wrong table would be worse than
			// declining: declining caps the finding to WARN.
			name:   "same name in two schemas is ambiguous, so unresolved",
			tables: []string{"public.users", "tenant.users"},
			lookup: "users", want: "users", wantOK: false,
		},
		{
			name:   "a qualified name the fixture does not have stays unresolved",
			tables: []string{"public.users"},
			lookup: "other.users", want: "other.users", wantOK: false,
		},
		{
			name:   "an unknown table stays unresolved",
			tables: []string{"public.users"},
			lookup: "widgets", want: "widgets", wantOK: false,
		},
		{
			name:   "no schema prefix in the fixture key",
			tables: []string{"users"},
			lookup: "users", want: "users", wantOK: true,
		},
		{
			name:   "empty name",
			tables: []string{"public.users"},
			lookup: "", want: "", wantOK: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := tablesFixture(c.tables...).ResolveTable(c.lookup)
			if got != c.want || ok != c.wantOK {
				t.Errorf("ResolveTable(%q) over %v = (%q, %v), want (%q, %v)",
					c.lookup, c.tables, got, ok, c.want, c.wantOK)
			}
		})
	}
}

// TestResolveTableNilSafe: analyzers call this on whatever the SQL parser handed
// them, so it must not panic on the degenerate cases.
func TestResolveTableNilSafe(t *testing.T) {
	var f *Fixture
	if got, ok := f.ResolveTable("users"); got != "users" || ok {
		t.Errorf("nil fixture = (%q, %v), want (\"users\", false)", got, ok)
	}
	empty := &Fixture{}
	if got, ok := empty.ResolveTable("users"); got != "users" || ok {
		t.Errorf("empty fixture = (%q, %v), want (\"users\", false)", got, ok)
	}
}
