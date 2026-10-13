package rotini

import (
	"strings"
	"testing"
)

func FuzzScopeIs(f *testing.F) {
	for _, s := range [][2]string{{"app", "app"}, {"app/serve", "app/serve"}, {"app/", "app/"}, {"a/b", "a/b/c"}, {"", ""}, {"/", "//x"}} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, names, scope string) {
		var chain []Command
		for n := range strings.SplitSeq(names, "|") {
			chain = append(chain, Command{Name: n})
		}
		joined := make([]string, len(chain))
		for i, c := range chain {
			joined[i] = c.Name
		}
		if got, want := scopeIs(scope, chain), scope == strings.Join(joined, "/"); got != want {
			t.Fatalf("scopeIs(%q, %q) = %t, want %t", scope, joined, got, want)
		}
	})
}
