package auth

import (
	"errors"
	"testing"
	"time"
)

func login(t *testing.T, s *Service, name string) string {
	t.Helper()
	token, _, err := s.Login(name, "password1")
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestLoginFailuresLookTheSame(t *testing.T) {
	s := newTestService(t)
	register(t, s, "alice")
	if _, _, err := s.Login("alice", "wrongpassword"); !errors.Is(err, ErrBadLogin) {
		t.Errorf("wrong password: %v", err)
	}
	if _, _, err := s.Login("nobody", "password1"); !errors.Is(err, ErrBadLogin) {
		t.Errorf("unknown: %v", err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	s := newTestService(t)
	a := register(t, s, "alice")

	token := login(t, s, "ALICE") // names match whatever the case
	got, err := s.Authenticate(token)
	if err != nil || got.ID != a.ID {
		t.Fatalf("got %+v %v", got, err)
	}
	var stored int
	s.db.QueryRow(`select count(*) from sessions where token_hash = $1`, token).Scan(&stored)
	if stored != 0 {
		t.Error("token stored in the clear")
	}

	if err := s.Logout(token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(token); !errors.Is(err, ErrNoSession) {
		t.Errorf("after logout: %v", err)
	}

	token = login(t, s, "alice")
	if err := s.ChangePassword(a.ID, "password2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(token); !errors.Is(err, ErrNoSession) {
		t.Errorf("after password change: %v", err)
	}
	if _, _, err := s.Login("alice", "password1"); !errors.Is(err, ErrBadLogin) {
		t.Errorf("old password: %v", err)
	}

	token, _, err = s.Login("alice", "password2")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDisabled(a.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(token); !errors.Is(err, ErrNoSession) {
		t.Errorf("disabled: %v", err)
	}
	if _, _, err := s.Login("alice", "password2"); !errors.Is(err, ErrBadLogin) {
		t.Errorf("disabled login: %v", err)
	}
}

func TestResetPassword(t *testing.T) {
	s := newTestService(t)
	a := register(t, s, "alice")
	token := login(t, s, "alice")
	temp, err := s.ResetPassword(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(token); !errors.Is(err, ErrNoSession) {
		t.Errorf("old session: %v", err)
	}
	_, got, err := s.Login("alice", temp)
	if err != nil {
		t.Fatal(err)
	}
	if !got.MustChange {
		t.Error("should have to change it")
	}
	if err := s.ChangePassword(a.ID, "password3"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(a.ID); got.MustChange {
		t.Error("should be done changing")
	}
}

func TestResetRoot(t *testing.T) {
	s := newTestService(t)
	if err := s.EnsureRoot("root", "password1"); err != nil {
		t.Fatal(err)
	}
	token := login(t, s, "root")
	name, temp, err := s.ResetRoot()
	if err != nil || name != "root" {
		t.Fatal(name, err)
	}
	if _, err := s.Authenticate(token); !errors.Is(err, ErrNoSession) {
		t.Errorf("old session: %v", err)
	}
	if _, _, err := s.Login("root", temp); err != nil {
		t.Error(err)
	}
}

func TestSessionExpiryAndRenewal(t *testing.T) {
	s := newTestService(t)
	register(t, s, "alice")
	now := time.Now()
	s.now = func() time.Time { return now }
	token := login(t, s, "alice")

	now = now.Add(SessionTTL - time.Hour) // nearly out: using it renews it
	if _, err := s.Authenticate(token); err != nil {
		t.Fatal(err)
	}
	now = now.Add(SessionTTL - time.Hour)
	if _, err := s.Authenticate(token); err != nil {
		t.Fatalf("should have renewed: %v", err)
	}
	now = now.Add(SessionTTL + time.Hour)
	if _, err := s.Authenticate(token); !errors.Is(err, ErrNoSession) {
		t.Errorf("expired: %v", err)
	}
}

func TestLimiter(t *testing.T) {
	now := time.Now()
	l := NewLimiter(5, 15*time.Minute)
	l.now = func() time.Time { return now }
	for range 5 {
		if !l.Allow("k") {
			t.Fatal("blocked early")
		}
		l.Fail("k")
	}
	if l.Allow("k") {
		t.Error("should be blocked")
	}
	if !l.Allow("other") {
		t.Error("other key blocked")
	}
	now = now.Add(16 * time.Minute)
	if !l.Allow("k") {
		t.Error("should have recovered")
	}
	l.Fail("k")
	l.Reset("k")
	for range 4 {
		l.Fail("k")
	}
	if !l.Allow("k") {
		t.Error("reset should clear failures")
	}
}
