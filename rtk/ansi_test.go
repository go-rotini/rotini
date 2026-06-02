package rtk

import "testing"

func TestStripANSI(t *testing.T) {
	styled := Style("hi", ColorTrueColor, RGB(1, 2, 3), Bold)
	if got := StripANSI(styled); got != "hi" {
		t.Errorf("StripANSI(styled) = %q, want %q", got, "hi")
	}
	if got := StripANSI("plain text"); got != "plain text" {
		t.Errorf("StripANSI(plain) = %q, want unchanged", got)
	}
	if got := StripANSI(ClearLine + "x" + CursorUp(2) + HideCursor); got != "x" {
		t.Errorf("StripANSI(controls) = %q, want %q", got, "x")
	}
}

func TestCursorSequences(t *testing.T) {
	cases := map[string]string{
		CursorUp(3):      "\x1b[3A",
		CursorUp(0):      "", // non-positive → empty
		CursorDown(1):    "\x1b[1B",
		CursorForward(5): "\x1b[5C",
		CursorBack(2):    "\x1b[2D",
		CursorColumn(10): "\x1b[10G",
		CursorColumn(0):  "\x1b[1G", // clamped to column 1
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("cursor sequence = %q, want %q", got, want)
		}
	}
}

func TestControlConstants(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{ClearLine, "\x1b[2K"},
		{ClearToLineEnd, "\x1b[0K"},
		{HideCursor, "\x1b[?25l"},
		{ShowCursor, "\x1b[?25h"},
		{Reset, "\x1b[0m"},
	} {
		if c.got != c.want {
			t.Errorf("control constant = %q, want %q", c.got, c.want)
		}
	}
}
