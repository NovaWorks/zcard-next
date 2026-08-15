package db

import "testing"

// 三方言矩阵：同一原语在不同方言下的输出必须符合各自语法（ADR-D20 §10）。
func TestSQLPrimitives(t *testing.T) {
	for _, tc := range []struct {
		d        Dialect
		ph1      string
		quote    string
		paginate string
		forUpd   string
	}{
		{MySQL, "?", "`id`", "LIMIT 0, 20", "FOR UPDATE"},
		{Postgres, "$1", `"id"`, "LIMIT 20 OFFSET 0", "FOR UPDATE"},
		{SQLite, "?", `"id` + `"`, "LIMIT 20 OFFSET 0", ""},
	} {
		t.Run(string(tc.d), func(t *testing.T) {
			s := New(tc.d)
			if got := s.Placeholder(1); got != tc.ph1 {
				t.Errorf("Placeholder(1) = %q, want %q", got, tc.ph1)
			}
			if got := s.QuoteIdent("id"); got != tc.quote {
				t.Errorf("QuoteIdent = %q, want %q", got, tc.quote)
			}
			if got := s.Paginate(20, 0); got != tc.paginate {
				t.Errorf("Paginate = %q, want %q", got, tc.paginate)
			}
			if got := s.ForUpdate(false); got != tc.forUpd {
				t.Errorf("ForUpdate = %q, want %q", got, tc.forUpd)
			}
		})
	}
}

func TestILike(t *testing.T) {
	if got := New(Postgres).ILike("name"); got != "name ILIKE ?" {
		t.Errorf("PG ILike = %q", got)
	}
	if got := New(MySQL).ILike("name"); got != "LOWER(name) LIKE LOWER(?)" {
		t.Errorf("MySQL ILike = %q", got)
	}
}

func TestDetect(t *testing.T) {
	for in, want := range map[string]Dialect{
		"mysql": MySQL, "MariaDB": MySQL, "postgres": Postgres, "PGX": Postgres,
		"sqlite": SQLite, "sqlite3": SQLite,
	} {
		got, err := Detect(in)
		if err != nil || got != want {
			t.Errorf("Detect(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := Detect("oracle"); err == nil {
		t.Error("未知驱动应报错")
	}
}

func TestCapabilities(t *testing.T) {
	if !Postgres.Capabilities().SupportsRLS {
		t.Error("PG 应支持 RLS")
	}
	if MySQL.Capabilities().SupportsRLS || SQLite.Capabilities().SupportsRLS {
		t.Error("MySQL/SQLite 不应支持 RLS")
	}
	if SQLite.Capabilities().SupportsForUpdate {
		t.Error("SQLite 无 FOR UPDATE（走 BEGIN IMMEDIATE + CAS）")
	}
	if !MySQL.Capabilities().SupportsSkipLocked || !Postgres.Capabilities().SupportsSkipLocked {
		t.Error("MySQL8/PG 应支持 SKIP LOCKED")
	}
}
