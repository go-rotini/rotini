package rotini

import (
	"path/filepath"
	"slices"
	"strings"
)

// roleChdir is the flag role ([FlagDef.Role]) the runtime acts on: a git-style `-C dir`.
const roleChdir = "chdir"

// chdirFlag returns the root's `role: chdir` flag, if it declares one.
func chdirFlag(def Definition) (FlagDef, bool) {
	i := slices.IndexFunc(def.Flags, func(f FlagDef) bool { return f.Role == roleChdir })
	if i < 0 {
		return FlagDef{}, false
	}
	return def.Flags[i], true
}

// chdirSite is one occurrence of the chdir flag in argv, typed as typed: the word at index at
// holds its value, as its tail when attached (`--dir=x`, `-Cx`), else as the whole word.
type chdirSite struct {
	flag     int // the index of the flag's own word
	at       int
	attached bool
	value    string
	typed    string
}

// chdirSites finds each occurrence of the root's chdir flag in argv, walked against chain the
// way the parser walks it: flags stop at "--", at an options_first command's first argument
// and where a passthrough command or argument starts. A word that doesn't tokenize is skipped;
// the parse reports it.
func chdirSites(chain []Command, argv []string) []chdirSite {
	if len(chain) == 0 {
		return nil
	}
	var sites []chdirSite
	leaf := len(chain) - 1
	pt := passthroughArg(chain[leaf].Arguments)
	depth, leafWords := 1, 0
	startedArgs := false
	for i := 0; i < len(argv); i++ {
		tok := argv[i]
		if chain[leaf].Passthrough && depth == len(chain) {
			break
		}
		if tok == "--" {
			break
		}
		if isFlag(chain[:depth], tok) {
			found := -1
			extra, err := consumeFlagToken(chain[:depth], tok, argv, i, func(_ int, fd FlagDef, value, typed string) error {
				if fd.Role == roleChdir {
					found = len(sites)
					sites = append(sites, chdirSite{flag: i, at: i, attached: true, value: value, typed: typed})
				}
				return nil
			})
			if err != nil {
				continue // the parse reports it; a later -C still counts
			}
			if found >= 0 && extra > 0 {
				sites[found].at, sites[found].attached = i+extra, false
			}
			i += extra
			continue
		}
		if !startedArgs && depth < len(chain) {
			if c, ok := findChild(chain[depth-1], tok); ok && c.Name == chain[depth].Name {
				depth++
				continue
			}
		}
		startedArgs = true
		if depth == len(chain) && leafWords == pt {
			break
		}
		leafWords++
		if chain[leaf].OptionsFirst && depth == len(chain) {
			break
		}
	}
	return sites
}

// applyChdir runs the chdir flag before anything else reads the run's directory: it finds the
// last occurrence in argv (words parsed against chain), resolves its value against the run's
// directory, checks that it is a directory, and makes it the run's directory. It returns argv
// with that occurrence's value replaced by the absolute directory, so the parse binds and
// checks that, and the earlier occurrences, which the last overrides, left out; or a usage
// error. With no chdir flag declared or given, argv is returned as is.
func (p *Program) applyChdir(rtx *Context, chain []Command, argv []string) ([]string, error) {
	fd, ok := chdirFlag(p.def)
	if !ok {
		return argv, nil
	}
	sites := chdirSites(chain, argv)
	if len(sites) == 0 {
		return argv, nil
	}
	last := sites[len(sites)-1]
	dir, err := rtx.enterChdir(last)
	if err != nil {
		return argv, err
	}

	out := slices.Clone(argv)
	if last.attached {
		out[last.at] = strings.TrimSuffix(out[last.at], last.value) + dir
	} else {
		out[last.at] = dir
	}
	if fd.NoRepeat || len(sites) == 1 {
		return out, nil // a flag that refuses repeats keeps them, for the parse to report
	}
	drop := map[int]bool{}
	for _, s := range sites[:len(sites)-1] {
		if strings.HasPrefix(out[s.flag], s.typed) { // not inside a cluster of other flags
			drop[s.flag], drop[s.at] = true, true
		}
	}
	kept := out[:0]
	for i, w := range out {
		if !drop[i] {
			kept = append(kept, w)
		}
	}
	return kept, nil
}

// withoutChdir returns words without the chdir flag's occurrences, its value included. The
// flags typed before a plugin's name are checked strictly, since the plugin never sees them;
// the directory is the exception, because it applies to the plugin's run too.
func withoutChdir(chain []Command, words []string) []string {
	drop := map[int]bool{}
	for _, s := range chdirSites(chain, words) {
		if strings.HasPrefix(words[s.flag], s.typed) { // not inside a cluster of other flags
			drop[s.flag], drop[s.at] = true, true
		}
	}
	if len(drop) == 0 {
		return words
	}
	out := make([]string, 0, len(words)-len(drop))
	for i, w := range words {
		if !drop[i] {
			out = append(out, w)
		}
	}
	return out
}

// enterChdir resolves the chdir flag's value at s against the run's directory, checks that it
// is a directory, and makes it the run's directory, returning it.
func (rtx *Context) enterChdir(s chdirSite) (string, error) {
	view := rtx.osView()
	base, err := view.getwd()
	if err != nil {
		return "", InternalError(err)
	}
	if err := checkPathExists(s.typed, "existingdir", s.value, base); err != nil {
		return "", err
	}
	dir := filepath.Clean(joinDir(base, s.value))
	rtx.mu.Lock()
	rtx.view = view.withDir(dir)
	rtx.mu.Unlock()
	return dir, nil
}

// completionChdir makes the chdir flag among the words typed so far the directory completers
// see. A bad one is left to the run that follows, which reports it.
func (p *Program) completionChdir(rtx *Context, typed []string) {
	if _, ok := chdirFlag(p.def); !ok || len(typed) == 0 {
		return
	}
	chain, _ := resolveChain(p.def, typed)
	if sites := chdirSites(chain, typed); len(sites) > 0 {
		if _, err := rtx.enterChdir(sites[len(sites)-1]); err != nil {
			return // the run reports it
		}
	}
}
