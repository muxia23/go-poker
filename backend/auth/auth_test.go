package auth

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegister(t *testing.T) {
	s := newTestService(t)
	code := invite(t, s)
	a, err := s.Register("Alice", "password1", code)
	if err != nil {
		t.Fatal(err)
	}
	if a.Role != RoleUser || a.ID == "" || a.Username != "Alice" {
		t.Errorf("got %+v", a)
	}
	if _, err := s.Register("bob", "password1", code); !errors.Is(err, ErrBadInvite) {
		t.Errorf("reused code: %v", err)
	}
	if _, err := s.Register("bob", "password1", "nonsense"); !errors.Is(err, ErrBadInvite) {
		t.Errorf("wrong code: %v", err)
	}
}

func TestExpiredAndRevokedInvites(t *testing.T) {
	s := newTestService(t)
	invite(t, s)
	root := s.mustRoot(t)
	expired, err := s.CreateInvite(root, -time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Register("bob", "password1", expired); !errors.Is(err, ErrBadInvite) {
		t.Errorf("expired: %v", err)
	}
	revoked, _ := s.CreateInvite(root, time.Hour)
	list, err := s.ListInvites()
	if err != nil {
		t.Fatal(err)
	}
	var newest Invite
	for _, i := range list {
		if i.CreatedAt >= newest.CreatedAt {
			newest = i
		}
	}
	if err := s.RevokeInvite(newest.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Register("bob", "password1", revoked); !errors.Is(err, ErrBadInvite) {
		t.Errorf("revoked: %v", err)
	}
}

func TestFailedRegisterKeepsInvite(t *testing.T) {
	s := newTestService(t)
	register(t, s, "alice")
	code := invite(t, s)
	if _, err := s.Register("ALICE", "password1", code); !errors.Is(err, ErrUsernameTaken) {
		t.Errorf("taken: %v", err)
	}
	if _, err := s.Register("bob", "short", code); !errors.Is(err, ErrBadPassword) {
		t.Errorf("weak: %v", err)
	}
	if _, err := s.Register("a b", "password1", code); !errors.Is(err, ErrBadUsername) {
		t.Errorf("bad name: %v", err)
	}
	if _, err := s.Register("bob", "password1", code); err != nil {
		t.Errorf("code should be unused: %v", err)
	}
}

func TestInviteOnlyWorksOnce(t *testing.T) {
	s := newTestService(t)
	code := invite(t, s)
	var ok atomic.Int32
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Register("user"+string(rune('a'+i)), "password1", code); err == nil {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 {
		t.Errorf("%d registrations", ok.Load())
	}
}

func TestNameOnlyTakenOnce(t *testing.T) {
	s := newTestService(t)
	codes := make([]string, 20)
	for i := range codes {
		codes[i] = invite(t, s)
	}
	var ok atomic.Int32
	var wg sync.WaitGroup
	for _, code := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Register("same", "password1", code); err == nil {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 {
		t.Errorf("%d registrations", ok.Load())
	}
}

func TestRename(t *testing.T) {
	s := newTestService(t)
	a := register(t, s, "alice")
	register(t, s, "bob")
	if err := s.Rename(a.ID, "BOB"); !errors.Is(err, ErrUsernameTaken) {
		t.Errorf("taken: %v", err)
	}
	if err := s.Rename(a.ID, "a b"); !errors.Is(err, ErrBadUsername) {
		t.Errorf("bad: %v", err)
	}
	if err := s.Rename(a.ID, "Alice"); err != nil {
		t.Errorf("own name, new case: %v", err)
	}
	if err := s.Rename(a.ID, "carol"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(a.ID)
	if err != nil || got.ID != a.ID || got.Username != "carol" {
		t.Errorf("got %+v %v", got, err)
	}
	// the old name is free again
	if _, err := s.Register("alice", "password1", invite(t, s)); err != nil {
		t.Errorf("old name: %v", err)
	}
}

func TestRootProtected(t *testing.T) {
	s := newTestService(t)
	invite(t, s)
	root := s.mustRoot(t)
	if err := s.SetDisabled(root, true); !errors.Is(err, ErrRootProtected) {
		t.Errorf("disable: %v", err)
	}
	if err := s.SetRole(root, RoleUser); !errors.Is(err, ErrRootProtected) {
		t.Errorf("demote: %v", err)
	}
	a := register(t, s, "alice")
	if err := s.SetRole(a.ID, RoleRoot); !errors.Is(err, ErrRootProtected) {
		t.Errorf("second root: %v", err)
	}
	if err := s.SetRole(a.ID, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(a.ID); got.Role != RoleAdmin {
		t.Errorf("role %q", got.Role)
	}
}

func TestEnsureRootOnlyOnce(t *testing.T) {
	s := newTestService(t)
	if err := s.EnsureRoot("first", "password1"); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureRoot("second", "password2"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Login("second", "password2"); !errors.Is(err, ErrBadLogin) {
		t.Errorf("second root: %v", err)
	}
	_, a, err := s.Login("first", "password1")
	if err != nil {
		t.Fatal(err)
	}
	if !a.MustChange || a.Role != RoleRoot {
		t.Errorf("got %+v", a)
	}
}

func TestEnsureRootNeedsCredentials(t *testing.T) {
	s := newTestService(t)
	if err := s.EnsureRoot("", ""); err == nil {
		t.Error("no root and no credentials should fail")
	}
}
