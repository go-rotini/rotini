package rotini

import (
	"strings"
	"testing"
)

func TestEscapeGlobMatchesReplacer(t *testing.T) {
	ref := strings.NewReplacer("[", "[[]", "*", "[*]", "?", "[?]")
	alphabet := []string{"[", "]", "*", "?", "a", "\\", "é", "[*]"}
	var gen func(prefix string, n int)
	gen = func(prefix string, n int) {
		if got, want := escapeGlob(prefix), ref.Replace(prefix); got != want {
			t.Fatalf("%q: got %q want %q", prefix, got, want)
		}
		if n > 0 {
			for _, a := range alphabet {
				gen(prefix+a, n-1)
			}
		}
	}
	gen("", 5)
}
