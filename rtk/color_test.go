package rtk

import "testing"

func lookFrom(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestDetectColorLevel(t *testing.T) {
	cases := []struct {
		name string
		tty  bool
		env  map[string]string
		want ColorLevel
	}{
		{"non-tty, no env → none", false, nil, ColorNone},
		{"tty, no env → 16", true, nil, Color16},
		{"NO_COLOR wins over everything", true, map[string]string{"NO_COLOR": "1", "COLORTERM": "truecolor"}, ColorNone},
		{"NO_COLOR empty is ignored", true, map[string]string{"NO_COLOR": "", "COLORTERM": "truecolor"}, ColorTrueColor},
		{"FORCE_COLOR=0 disables even on tty", true, map[string]string{"FORCE_COLOR": "0", "TERM": "xterm-256color"}, ColorNone},
		{"FORCE_COLOR=2 → 256 off-tty", false, map[string]string{"FORCE_COLOR": "2"}, Color256},
		{"FORCE_COLOR=3 → truecolor off-tty", false, map[string]string{"FORCE_COLOR": "3"}, ColorTrueColor},
		{"FORCE_COLOR=1 off-tty → forced floor 16", false, map[string]string{"FORCE_COLOR": "1"}, Color16},
		{"CLICOLOR_FORCE forces off-tty, level from env", false, map[string]string{"CLICOLOR_FORCE": "1", "COLORTERM": "truecolor"}, ColorTrueColor},
		{"CLICOLOR_FORCE=0 does not force", false, map[string]string{"CLICOLOR_FORCE": "0", "COLORTERM": "truecolor"}, ColorNone},
		{"CLICOLOR=0 disables on tty", true, map[string]string{"CLICOLOR": "0", "COLORTERM": "truecolor"}, ColorNone},
		{"COLORTERM truecolor on tty", true, map[string]string{"COLORTERM": "truecolor"}, ColorTrueColor},
		{"COLORTERM 24bit on tty", true, map[string]string{"COLORTERM": "24bit"}, ColorTrueColor},
		{"TERM 256color on tty", true, map[string]string{"TERM": "screen-256color"}, Color256},
		{"TERM direct on tty → truecolor", true, map[string]string{"TERM": "xterm-direct"}, ColorTrueColor},
		{"TERM dumb on tty → none", true, map[string]string{"TERM": "dumb"}, ColorNone},
		{"TERM dumb but forced → 16", false, map[string]string{"TERM": "dumb", "CLICOLOR_FORCE": "1"}, Color16},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := detectColorLevel(c.tty, lookFrom(c.env)); got != c.want {
				t.Errorf("detectColorLevel(tty=%v, %v) = %s, want %s", c.tty, c.env, got, c.want)
			}
		})
	}
}

func TestColorLevel_String(t *testing.T) {
	for lvl, want := range map[ColorLevel]string{
		ColorNone: "none", Color16: "16", Color256: "256", ColorTrueColor: "truecolor", ColorLevel(99): "unknown",
	} {
		if got := lvl.String(); got != want {
			t.Errorf("ColorLevel(%d).String() = %q, want %q", lvl, got, want)
		}
	}
}
