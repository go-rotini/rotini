package rotini

import (
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// A line break outside quotes in a separated list value is an error, never a silent cut; one
// inside quotes is part of the item.
func TestSplitValueLineBreaks(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  []string // nil = error
	}{
		{"a,b\nc,d", nil},
		{"a\r\nb", nil},
		{"\nx", nil},
		{"a,b\n", nil},
		{`"a` + "\n" + `b",c`, []string{"a\nb", "c"}},
		{`"say ""hi""` + "\n" + `",c`, []string{"say \"hi\"\n", "c"}},
		{`"a"` + "\n" + `b`, nil},
		{`x"y` + "\n" + `z`, nil},
		{"a, b", []string{"a", "b"}},
	} {
		got, err := splitValue(tc.value, ",")
		switch {
		case tc.want == nil && err == nil:
			t.Errorf("splitValue(%q) = %q, want an error", tc.value, got)
		case tc.want != nil && (err != nil || !slices.Equal(got, tc.want)):
			t.Errorf("splitValue(%q) = %q, %v; want %q", tc.value, got, err, tc.want)
		}
	}

	def := Definition{Name: "app", Flags: []FlagDef{
		{Name: "tags", Identifiers: []string{"--tags"}, Type: "[]string", Separator: ","},
	}}
	var in struct {
		App struct {
			Flags struct {
				Tags []string `rotini:"tags"`
			}
		}
	}
	err := NewParser().Parse(NewContextFor(def, []string{"--tags", "a,b\nc,d"}), &in)
	if err == nil || !strings.Contains(err.Error(), "line break outside quotes") {
		t.Errorf("--tags with a line break: %v (tags %q), want an error", err, in.App.Flags.Tags)
	}
}

// FuzzSplitValue checks that a value without quotes either fails or splits into items that
// join back to it, so no part of it is ever dropped.
func FuzzSplitValue(f *testing.F) {
	for _, seed := range []string{"a,b", "a, b,,c", "a,b\nc,d", "\nx", `"a,b",c`, "a\r\nb"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		items, err := splitValue(value, ",")
		if err != nil || strings.Contains(value, `"`) || !utf8.ValidString(value) {
			return
		}
		parts := strings.Split(value, ",")
		for i, p := range parts {
			parts[i] = strings.TrimLeftFunc(p, unicode.IsSpace)
		}
		if value == "" {
			parts = nil
		}
		if !slices.Equal(items, parts) {
			t.Fatalf("splitValue(%q) = %q, want %q", value, items, parts)
		}
	})
}

// FuzzAcquiredLines checks that a file's or stdin's lines split into items that hold no newline
// and are not blank, and that NUL-separated items are kept byte for byte. Only one trailing "\r"
// is removed per line, so a line ending "\r\r\n" keeps one.
func FuzzAcquiredLines(f *testing.F) {
	for _, seed := range []string{"a\nb\n", "\xef\xbb\xbf# c\r\n x \n\n", "a\x00\x00b", "0\r\r"} {
		f.Add(seed, false, true)
	}
	f.Fuzz(func(t *testing.T, text string, nul, comments bool) {
		items := acquiredLines(text, nul, comments)
		for _, item := range items {
			if item == "" {
				t.Fatalf("acquiredLines(%q) kept an empty item", text)
			}
			if nul && strings.Contains(item, "\x00") {
				t.Fatalf("acquiredLines(%q) item %q holds a NUL", text, item)
			}
			if !nul && (strings.Contains(item, "\n") || strings.TrimSpace(item) == "") {
				t.Fatalf("acquiredLines(%q) item %q holds a newline or is blank", text, item)
			}
		}
		if nul && !strings.HasPrefix(text, utf8BOM) {
			if joined := strings.Join(items, ""); joined != strings.ReplaceAll(text, "\x00", "") {
				t.Fatalf("acquiredLines(%q) = %q, lost bytes", text, items)
			}
		}
	})
}

// FuzzMulticallArgv checks that multicall routing only ever puts one of the root's command or
// plugin spellings in front of argv, and otherwise leaves argv alone.
func FuzzMulticallArgv(f *testing.F) {
	for _, seed := range []string{"/usr/bin/ls", "acme-ls", `C:\bin\LS.EXE`, "app", "acme-", "kubectl_complete-x"} {
		f.Add(seed, false)
		f.Add(seed, true)
	}
	def := Definition{Name: "app",
		Multicall: &MulticallDef{Prefix: "acme-", Complete: "kubectl_complete-"},
		Commands:  []CommandDef{{Name: "ls", Aliases: []string{"list"}}, {Name: "app"}},
		Plugins:   []PluginDef{{Name: "ctx", Binary: "app-ctx"}},
	}
	f.Fuzz(func(t *testing.T, argv0 string, windows bool) {
		goos := "linux"
		if windows {
			goos = "windows"
		}
		argv := []string{"x", "--y"}
		routed, complete := multicallArgv(def, argv0, argv, goos)
		if complete || len(routed) == len(argv) {
			if !slices.Equal(routed, argv) {
				t.Fatalf("%q: routed %q, want argv unchanged", argv0, routed)
			}
			return
		}
		if len(routed) != len(argv)+1 || !slices.Equal(routed[1:], argv) {
			t.Fatalf("%q: routed %q, want one token in front of argv", argv0, routed)
		}
		root := rootFrame(def)
		_, child := findChild(&root, routed[0])
		_, plugin := findPlugin(&root, routed[0])
		if !child && !plugin {
			t.Fatalf("%q: routed to %q, which names no command or plugin", argv0, routed[0])
		}
	})
}
