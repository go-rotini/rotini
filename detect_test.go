package rotini

import (
	"os"
	"path/filepath"
	"testing"
)

// The detection helpers read the environment and the stream, which is why they went untested
// for so long: they are the seam a program uses to decide about color and interaction, and
// rotini never calls them itself.

func TestEnvNoColor(t *testing.T) {
	cases := []struct {
		name                 string
		noColor, clicolorFrc string
		set                  []string // which vars to set at all
		want                 bool
	}{
		{name: "neither set", want: false},
		{name: "NO_COLOR set", noColor: "1", set: []string{"NO_COLOR"}, want: true},
		{name: "NO_COLOR empty is not set", noColor: "", set: []string{"NO_COLOR"}, want: false},
		{name: "CLICOLOR_FORCE overrides NO_COLOR", noColor: "1", clicolorFrc: "1",
			set: []string{"NO_COLOR", "CLICOLOR_FORCE"}, want: false},
		{name: `CLICOLOR_FORCE="0" does not override`, noColor: "1", clicolorFrc: "0",
			set: []string{"NO_COLOR", "CLICOLOR_FORCE"}, want: true},
		{name: "CLICOLOR_FORCE alone", clicolorFrc: "1", set: []string{"CLICOLOR_FORCE"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", "")
			t.Setenv("CLICOLOR_FORCE", "")
			os.Unsetenv("NO_COLOR")
			os.Unsetenv("CLICOLOR_FORCE")
			for _, k := range tc.set {
				switch k {
				case "NO_COLOR":
					t.Setenv(k, tc.noColor)
				case "CLICOLOR_FORCE":
					t.Setenv(k, tc.clicolorFrc)
				}
			}
			if got := EnvNoColor(); got != tc.want {
				t.Errorf("EnvNoColor() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsTerminal(t *testing.T) {
	if IsTerminal(nil) {
		t.Error("IsTerminal(nil) = true, want false")
	}

	// A regular file is not a terminal — the case that matters, since it is what a
	// redirected run looks like.
	f, err := os.CreateTemp(t.TempDir(), "notatty")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if IsTerminal(f) {
		t.Error("a regular file reported as a terminal")
	}

	// A closed file cannot be stat'd; that is not a terminal either, and must not panic.
	closed, err := os.Open(filepath.Join(t.TempDir(), ".."))
	if err == nil {
		closed.Close()
		if IsTerminal(closed) {
			t.Error("a closed file reported as a terminal")
		}
	}

	// /dev/null IS a character device, so it reports true — worth pinning, because it is
	// the one "not really a terminal" that passes the check, and callers should know.
	if devNull, err := os.Open(os.DevNull); err == nil {
		defer devNull.Close()
		if !IsTerminal(devNull) {
			t.Errorf("%s is a character device; IsTerminal should report true", os.DevNull)
		}
	}
}
