package rotini

import "testing"

func TestStripStyles(t *testing.T) {
	styled := "\x1b[1mbold\x1b[0m \x1b[3mitalic\x1b[23m \x1b[38;5;208mcolor\x1b[39m"
	link := "see \x1b]8;;https://rotini.dev\x07rotini.dev\x1b]8;;\x07 docs"
	linkST := "see \x1b]8;;https://rotini.dev\x1b\\rotini.dev\x1b]8;;\x1b\\ docs"

	cases := []struct {
		name  string
		text  string
		bools []bool
		want  string
	}{
		{"no signals leaves text alone", styled, nil, styled},
		{"false signals leave text alone", styled, []bool{false, false}, styled},
		{"any true strips", styled, []bool{false, true}, "bold italic color"},
		{"hyperlink keeps visible text (BEL)", link, []bool{true}, "see rotini.dev docs"},
		{"hyperlink keeps visible text (ST)", linkST, []bool{true}, "see rotini.dev docs"},
		{"plain text unharmed", "plain text", []bool{true}, "plain text"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := StripStyles(c.text, c.bools...); got != c.want {
				t.Errorf("StripStyles(%q, %v) = %q, want %q", c.text, c.bools, got, c.want)
			}
		})
	}
}
