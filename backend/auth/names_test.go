package auth

import (
	"strings"
	"testing"
)

func TestNormalizeUsername(t *testing.T) {
	_, a, err := NormalizeUsername("Alice")
	if err != nil {
		t.Fatal(err)
	}
	_, b, _ := NormalizeUsername("alice")
	if a != b {
		t.Errorf("case: %q != %q", a, b)
	}
	_, c1, _ := NormalizeUsername("café")
	_, c2, _ := NormalizeUsername("café")
	if c1 != c2 {
		t.Errorf("unicode forms: %q != %q", c1, c2)
	}
	for _, ok := range []string{"老王", "a_b-1", "ab", strings.Repeat("a", 20), strings.Repeat("牌", 20)} {
		if _, _, err := NormalizeUsername(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "a", "a b", " ab", strings.Repeat("a", 21), "a@b", "System", "system", "a​b"} {
		if _, _, err := NormalizeUsername(bad); err != ErrBadUsername {
			t.Errorf("%q: got %v", bad, err)
		}
	}
}
