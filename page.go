package rotini

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
