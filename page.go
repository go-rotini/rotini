package rotini

// Page is one generated documentation page for one command: a man page or a markdown reference
// page. A CLI with the man feature on gets a generated ManPages function returning every
// command's man page, and with the markdown feature on, MarkdownPages; both list the visible
// commands in tree order, root first, so a `man` or `docs` command, a build script or a test can
// ship every page without keeping its own list of command paths.
//
//	for _, p := range cmd.ManPages() {
//		path := filepath.Join(dir, p.Name+"."+cmd.ManSection)
//		if err := os.WriteFile(path, []byte(p.Content), 0o644); err != nil {
//			return err
//		}
//	}
type Page struct {
	// Name is the page's name: the command path joined with "-" and lowercased, "taskr-add". It
	// is the name `man` looks a page up by, and a man page's file name without its extension.
	Name string
	// Path is the command path below the root, ["add"]; nil for the root command.
	Path []string
	// Content is the page itself: roff for a man page, markdown for a markdown page.
	Content string
}
