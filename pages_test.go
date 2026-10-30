package rotini

import "testing"

func TestStripANSI(t *testing.T) {
	styled := "\x1b[1mbold\x1b[0m \x1b[3mitalic\x1b[23m \x1b[38;5;208mcolor\x1b[39m"
	if got, want := StripANSI(styled), "bold italic color"; got != want {
		t.Errorf("stripANSI(styled) = %q, want %q", got, want)
	}
	// OSC-8 hyperlinks keep their visible text, in both terminator forms.
	link := "see \x1b]8;;https://example.dev\x07example.dev\x1b]8;;\x07 docs"
	linkST := "see \x1b]8;;https://example.dev\x1b\\example.dev\x1b]8;;\x1b\\ docs"
	if got, want := StripANSI(link), "see example.dev docs"; got != want {
		t.Errorf("stripANSI(BEL link) = %q, want %q", got, want)
	}
	if got, want := StripANSI(linkST), "see example.dev docs"; got != want {
		t.Errorf("stripANSI(ST link) = %q, want %q", got, want)
	}
	// Private CSI parameters, character-set designations, two-byte sequences and the other
	// control strings go too.
	for in, want := range map[string]string{
		"\x1b(Bplain\x1b[m":   "plain",
		"\x1b[>4;2mplain":     "plain",
		"\x1b[=5hplain":       "plain",
		"\x1bcplain":          "plain",
		"\x1b7plain\x1b8":     "plain",
		"\x1bPq#0\x1b\\plain": "plain",
		"\x1b_app\x07plain":   "plain",
		"\x1b[1":              "\x1b[1",
		"\x1bXsos\x1b\\plain": "plain",
		"\x1b^pm\x07plain":    "plain",
		"plain\x1b":           "plain\x1b",
		"\x1b\x01plain":       "\x1b\x01plain",
		"\x1b]8;;open":        "\x1b]8;;open",
		"\x1b(":               "\x1b(",
	} {
		if got := StripANSI(in); got != want {
			t.Errorf("StripANSI(%q) = %q, want %q", in, got, want)
		}
	}
	// Plain text is untouched.
	if got := StripANSI("plain"); got != "plain" {
		t.Errorf("stripANSI(plain) = %q, want unchanged", got)
	}
}
