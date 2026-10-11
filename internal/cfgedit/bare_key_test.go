package cfgedit

import (
	"regexp"
	"testing"
)

// regexpBareKey is the regular expression isBareKey replaced.
var regexpBareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func FuzzBareKey(f *testing.F) {
	for _, s := range []string{"", "a", "A-z_09", "a.b", "a b", "a\n", "\na", "é", "-", "a\x00", "😀"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, k string) {
		if got, want := isBareKey(k), regexpBareKey.MatchString(k); got != want {
			t.Fatalf("isBareKey(%q) = %v, want %v", k, got, want)
		}
	})
}
