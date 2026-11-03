package auth

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	file := filepath.Join(t.TempDir(), "t.db")
	db, err := sql.Open("sqlite", "file:"+file+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	schema, err := os.ReadFile("../server/migrations/003_accounts.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	s.cost = 4 // bcrypt.MinCost, so the tests stay quick
	return s
}

// invite makes a code from root, creating root the first time.
func invite(t *testing.T, s *Service) string {
	t.Helper()
	if err := s.EnsureRoot("root", "rootpassword"); err != nil {
		t.Fatal(err)
	}
	code, err := s.CreateInvite(s.mustRoot(t), DefaultInviteTTL)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func (s *Service) mustRoot(t *testing.T) string {
	t.Helper()
	var id string
	if err := s.db.QueryRow(`select id from accounts where role = 'root'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func register(t *testing.T, s *Service, name string) Account {
	t.Helper()
	a, err := s.Register(name, "password1", invite(t, s))
	if err != nil {
		t.Fatal(err)
	}
	return a
}
