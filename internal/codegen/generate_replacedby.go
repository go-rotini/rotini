package codegen

import "strings"

// A deprecated command's `replaced_by` is a path below the root command of the spec declaring
// it, so a composed child can name its own commands without knowing where it is mounted. It is
// resolved here to a path below the program's root, which the contract records, and rendered
// as the user types it, which the pages and the runtime show.

// replacement is a deprecated command's replacement: path is below the program's root
// ("db migrate"), text is how the user types it ("acme db migrate").
type replacement struct {
	path, text string
}

// commandReplacement resolves target, a path below the spec root at underscore path specRoot
// ("" for the program's own spec).
func (gp *program) commandReplacement(target, specRoot string) replacement {
	if target == "" {
		return replacement{}
	}
	path := target
	if specRoot != "" {
		path = strings.ReplaceAll(specRoot, "_", " ") + " " + target
	}
	return replacement{path: path, text: gp.rootDisplay + " " + path}
}

// overlayReplacement resolves the replacement of a composed spec's root as mounted: the one the
// `$ref` node declares, relative to the referring spec's root (outerRoot), else the composed
// root's own, relative to the composed spec (innerRoot).
func (gp *program) overlayReplacement(overlay, own, outerRoot, innerRoot string) replacement {
	if overlay != "" {
		return gp.commandReplacement(overlay, outerRoot)
	}
	return gp.commandReplacement(own, innerRoot)
}

// withReplacement appends "use <replacement> instead" to a deprecation message for the pages,
// which show it after the message: "(deprecated since 1.4.0: renamed; use taskr purge instead)".
func withReplacement(message, replacement string) string {
	if replacement == "" || message == "" {
		return message
	}
	return message + "; use " + replacement + " instead"
}

// overlaySpellings applies the `hidden_aliases` and `replaced_by` a `$ref` node declares to the
// mounted command m; see [overlayCommand].
func overlaySpellings(m *Command, parent Command) {
	if len(parent.HiddenAliases) > 0 {
		m.HiddenAliases = parent.HiddenAliases
	}
	if parent.ReplacedBy != "" {
		m.ReplacedBy = parent.ReplacedBy
	}
}
