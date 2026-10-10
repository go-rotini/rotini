package rotini

import (
	"errors"
	"os/user"
	"slices"
	"testing"
)

func TestExpandValue(t *testing.T) {
	view := newOSView([]string{"HOME=/home/ada", "USERPROFILE=C:\\Users\\ada", "A=/a", "B=$C", "T=~/x", "EMPTY="}, "", "linux")
	lookup := func(name string) (*user.User, error) {
		if name == "bob" {
			return &user.User{HomeDir: "/home/bob"}, nil
		}
		return nil, user.UnknownUserError(name)
	}
	both := expandRule{home: true, env: true}
	for name, tc := range map[string]struct {
		goos, in string
		rule     expandRule
		want     string
		err      string
	}{
		"tilde":                {in: "~", rule: both, want: "/home/ada"},
		"tilde slash":          {in: "~/x/y", rule: both, want: "/home/ada/x/y"},
		"tilde backslash":      {goos: "windows", in: `~\x`, rule: both, want: `C:\Users\ada\x`},
		"backslash elsewhere":  {in: `~\x`, rule: both, err: `cannot expand "~\\x": no such user`},
		"named user":           {in: "~bob/x", rule: both, want: "/home/bob/x"},
		"unknown user":         {in: "~nosuch/x", rule: both, err: `cannot expand "~nosuch": no such user`},
		"tilde inside":         {in: "a~/x", rule: both, want: "a~/x"},
		"home only":            {in: "~/$A", rule: expandRule{home: true}, want: "/home/ada/$A"},
		"env only":             {in: "~/$A", rule: expandRule{env: true}, want: "~//a"},
		"var":                  {in: "$A/x", rule: both, want: "/a/x"},
		"braced":               {in: "${A}x", rule: both, want: "/ax"},
		"unset":                {in: "$NOPE/x", rule: both, err: `$NOPE is not set (in "$NOPE/x")`},
		"empty":                {in: "${EMPTY}/x", rule: both, err: `$EMPTY is not set (in "${EMPTY}/x")`},
		"modifier":             {in: "${A:-x}", rule: both, err: `only $NAME and ${NAME} are expanded (in "${A:-x}")`},
		"unclosed":             {in: "${A", rule: both, err: `unclosed ${ in "${A"`},
		"digit after dollar":   {in: "$1", rule: both, want: "$1"},
		"trailing dollar":      {in: "C$", rule: both, want: "C$"},
		"single pass var":      {in: "$B", rule: both, want: "$C"},
		"single pass tilde":    {in: "$T", rule: both, want: "~/x"},
		"no rule":              {in: "~/$A", want: "~/$A"},
		"percent kept":         {goos: "windows", in: "%A%", rule: both, want: "%A%"},
		"underscore and digit": {in: "$A_1", rule: both, err: `$A_1 is not set (in "$A_1")`},
	} {
		t.Run(name, func(t *testing.T) {
			goos := tc.goos
			if goos == "" {
				goos = "linux"
			}
			v := view
			if goos == "windows" {
				v = newOSView([]string{"HOME=C:\\Users\\ada", "USERPROFILE=C:\\Users\\ada", "A=/a"}, "", "windows") // home() follows the running OS
			}
			got, err := expandValueFor(goos, tc.in, tc.rule, v, lookup)
			if tc.err != "" {
				if err == nil || err.Error() != tc.err {
					t.Fatalf("expand(%q) = %q, %v; want error %q", tc.in, got, err, tc.err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("expand(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		})
	}
}

func TestExpandValue_homeMissing(t *testing.T) {
	_, err := expandValueFor("linux", "~/x", expandRule{home: true}, newOSView([]string{}, "", "linux"), user.Lookup)
	if err == nil || err.Error() != `cannot expand "~/x": $HOME is not defined` {
		t.Errorf("err = %v", err)
	}
}

func TestExpandValue_currentUser(t *testing.T) {
	u, err := user.Current()
	if err != nil || u.Username == "" || u.HomeDir == "" {
		t.Skip("no current user")
	}
	got, err := expandValueFor("linux", "~"+u.Username+"/x", expandRule{home: true}, newOSView(nil, "", "linux"), user.Lookup)
	if err != nil || got != u.HomeDir+"/x" {
		t.Errorf("~%s/x = %q, %v", u.Username, got, err)
	}
}

func TestExpandValues_listAndLabel(t *testing.T) {
	view := newOSView([]string{"A=/a", "B=/b"}, "", "linux")
	in := []string{"$A", "$B/x", "plain"}
	out, err := expandValues(in, expandRule{env: true}, view, "--paths")
	if err != nil || !slices.Equal(out, []string{"/a", "/b/x", "plain"}) || in[0] != "$A" {
		t.Fatalf("out = %q, %v (input %q)", out, err, in)
	}
	unchanged := []string{"x"}
	if out, _ := expandValues(unchanged, expandRule{env: true}, view, "--x"); &out[0] != &unchanged[0] {
		t.Error("an unchanged list should be returned as is")
	}
	_, err = expandValues([]string{"$NOPE"}, expandRule{env: true}, view, "--paths")
	if err == nil || err.Error() != `--paths: $NOPE is not set (in "$NOPE")` {
		t.Errorf("err = %v", err)
	}
	if _, ok := errors.AsType[*expandError](err); !ok {
		t.Errorf("err = %T, want *expandError", err)
	}
}

func TestRelativeToFile(t *testing.T) {
	got := relativeToFile([]string{"cache", "/abs", "-", ""}, "/etc/app/config.yaml")
	want := []string{"/etc/app/cache", "/abs", "-", ""}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := relativeToFile([]string{"x"}, ""); got[0] != "x" {
		t.Errorf("no file: %q", got)
	}
}

func TestTagExpandRule(t *testing.T) {
	r := tagExpandRule(`expand:"home,env" relativeto:"config"`)
	if !r.home || !r.env || !r.relConfig || !r.expands() {
		t.Errorf("rule = %+v", r)
	}
	if r := tagExpandRule(`relativeto:"config"`); r.expands() || !r.declared() {
		t.Errorf("relative only = %+v", r)
	}
}
