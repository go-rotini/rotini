package rotini

import (
	"regexp"
	"strings"
	"sync"
)

// ansiSequences matches CSI sequences (SGR styling among them) and OSC sequences terminated by
// BEL or ST.
var ansiSequences = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`\x1b\[[0-9;:?]*[\x20-\x2f]*[\x40-\x7e]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
})

// esc begins every ANSI escape sequence; text without one is returned without running the regexp.
const esc = '\x1b'

// StripANSI removes every ANSI escape sequence from text, SGR styling and OSC alike, leaving the
// characters a terminal would display. Rotini applies it to man pages, markdown pages and
// completion descriptions, whose consumers would print escapes literally.
func StripANSI(text string) string {
	if !strings.ContainsRune(text, esc) {
		return text
	}
	return ansiSequences().ReplaceAllString(text, "")
}

// Page is one generated documentation page for one command: a man page or a markdown reference
// page. With the man feature on, codegen emits a ManPages function returning every command's man
// page; with the markdown feature on, MarkdownPages. Both list the visible commands in tree
// order, root first.
//
//	for _, p := range cmd.ManPages() {
//		path := filepath.Join(dir, p.Name+"."+cmd.ManSection)
//		if err := os.WriteFile(path, []byte(p.Content), 0o644); err != nil {
//			return err
//		}
//	}
type Page struct {
	// Name is the command path joined with "-" and lowercased ("taskr-add"): the name `man`
	// looks the page up by, and the man page's file name without its extension.
	Name string
	// Path is the command path below the root, ["add"]; nil for the root command.
	Path []string
	// Content is the page itself: roff for a man page, markdown for a markdown page.
	Content string
}
