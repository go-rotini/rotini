package codegen

import (
	"bytes"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-rotini/rotini/internal/cfgedit"
	"github.com/go-rotini/yaml"
)

// MountFn is the signature of [Mount], for the companion CLI's test doubles.
type MountFn func(into string, specs []string, opts MountOptions) (MountResult, error)

// MountOptions are the settings of one [Mount] run.
type MountOptions struct {
	Under  []string // the command path, below the root, whose commands to edit; nil for the root
	Group  string   // written next to each new $ref; "" writes none
	Prune  bool     // remove the $ref entries whose file is gone
	DryRun bool     // write nothing
}

// MountEntry is one mounted spec: the command name it mounts as and the $ref that mounts it.
type MountEntry struct {
	Name string
	Ref  string
}

// MountResult is what a [Mount] run did, or would have done with DryRun.
type MountResult struct {
	Mounted []MountEntry // added, in the order given
	Already []MountEntry // given, and mounted before the run
	Removed []string     // the $refs pruned, as written
	Skipped []string     // given paths that name the parent spec itself
}

// Changed reports whether the run changed the parent spec, or would have.
func (r MountResult) Changed() bool { return len(r.Mounted) > 0 || len(r.Removed) > 0 }

// mountedCommand is one entry of the parent's commands list.
type mountedCommand struct {
	item  *cfgedit.YAMLItem
	ref   string   // "" for an inline command
	abs   string   // a local $ref's absolute path
	names []string // the name and aliases it takes, when known
	gone  bool     // a local $ref whose file doesn't exist
}

// Mount adds a $ref entry to the YAML spec into for each spec in specs, after its last $ref
// entry (or at the end of its commands list). A spec already mounted is left as it is, and the
// path of into itself is skipped. With Prune, entries whose local file doesn't exist are
// removed. Every other byte of into is kept. A command name or alias that two entries would
// share is an error, and nothing is written.
func Mount(into string, specs []string, opts MountOptions) (MountResult, error) {
	switch ext := strings.ToLower(filepath.Ext(into)); ext {
	case ".yaml", ".yml":
	default:
		return MountResult{}, fmt.Errorf("%s: rotini mount edits YAML specs only", into)
	}
	parentAbs, err := filepath.Abs(into)
	if err != nil {
		return MountResult{}, fmt.Errorf("resolve %s: %w", into, err)
	}
	src, err := os.ReadFile(into)
	if err != nil {
		return MountResult{}, fmt.Errorf("read %s: %w", into, err)
	}
	doc, err := cfgedit.IndexYAML(src)
	if err != nil {
		return MountResult{}, fmt.Errorf("%s: %w", into, err)
	}
	t, err := findTarget(doc, into, opts.Under, filepath.Dir(parentAbs))
	if err != nil {
		return MountResult{}, err
	}
	existing, err := mountedCommands(t.list, filepath.Dir(parentAbs))
	if err != nil {
		return MountResult{}, fmt.Errorf("%s: %w", into, err)
	}
	var own []string
	if t.abs != "" {
		if own, err = refCommandNames(t.abs); err != nil {
			return MountResult{}, fmt.Errorf("%s: %w", into, err)
		}
	}

	res, err := planMounts(into, parentAbs, specs, existing, own, t, opts.Prune)
	if err != nil {
		return MountResult{}, err
	}
	var removed []mountedCommand
	if opts.Prune {
		for _, c := range existing {
			if c.gone {
				removed = append(removed, c)
				res.Removed = append(res.Removed, c.ref)
			}
		}
	}
	if !res.Changed() {
		return res, nil
	}

	out := spliceMounts(doc, t.cmd, t.list, existing, removed, res.Mounted, opts.Group)
	if err := checkMounts(out, into, t, existing, removed, res.Mounted); err != nil {
		return MountResult{}, err
	}
	if opts.DryRun {
		return res, nil
	}
	if err := newPlanner(false).write(parentAbs, out); err != nil {
		return MountResult{}, err
	}
	return res, nil
}

// planMounts sorts the given specs into new, already mounted and skipped, and checks that no
// command name or alias would be taken twice. Entries pruned don't hold their names.
func planMounts(into, parentAbs string, specs []string, existing []mountedCommand, own []string, t mountTarget, prune bool) (MountResult, error) {
	var res MountResult
	taken := map[string]string{} // name or alias → who takes it
	for _, n := range own {
		taken[n] = fmt.Sprintf("%s's own command %q", t.ref, n)
	}
	for name, tokens := range pluginTokens(t.cmd) {
		for _, n := range tokens {
			taken[n] = fmt.Sprintf("the plugin %q", name)
		}
	}
	for _, c := range existing {
		if c.gone && prune {
			continue
		}
		for _, n := range c.names {
			taken[n] = takenBy(c.ref, c.names[0])
		}
	}
	var errs []error
	for _, spec := range specs {
		entry, abs, names, err := mountEntry(spec, filepath.Dir(parentAbs))
		switch {
		case err != nil:
			errs = append(errs, err)
			continue
		case abs == parentAbs:
			res.Skipped = append(res.Skipped, spec)
			continue
		case abs == t.abs:
			errs = append(errs, fmt.Errorf("%s: can't mount a spec under itself (%s)", spec, t.where))
			continue
		case slices.ContainsFunc(res.Mounted, func(m MountEntry) bool { return m.Ref == entry.Ref }):
			continue
		}
		if i := slices.IndexFunc(existing, func(c mountedCommand) bool { return c.abs == abs }); i >= 0 {
			res.Already = append(res.Already, MountEntry{Name: entry.Name, Ref: existing[i].ref})
			continue
		}
		var clashes []error
		for _, n := range names {
			if who, ok := taken[n]; ok {
				clashes = append(clashes, fmt.Errorf("%s: the command name %q is already taken by %s under %s in %s", spec, n, who, t.where, into))
			}
		}
		if len(clashes) > 0 {
			errs = append(errs, clashes...)
			continue
		}
		for _, n := range names {
			taken[n] = spec
		}
		res.Mounted = append(res.Mounted, entry)
	}
	return res, errors.Join(errs...)
}

// takenBy names the entry taking a command name in an error: its $ref, else the inline command.
func takenBy(ref, name string) string {
	if ref != "" {
		return ref
	}
	return fmt.Sprintf("the command %q", name)
}

// errNoCommands is commandsOf's answer for a command with no commands list yet.
var errNoCommands = errors.New("no commands list")

// mountTarget is the command whose commands list a run edits.
type mountTarget struct {
	cmd   *cfgedit.YAMLNode // its mapping
	list  *cfgedit.YAMLPair // its commands list; nil when it has none
	path  []int             // the item index at each level below the root
	where string            // "acme db", for messages
	ref   string            // when it is a $ref entry: the $ref, as written
	abs   string            // and the spec it mounts, when local
}

// findTarget walks from the root command down the commands named in under, by name or alias,
// and returns the last. A step can only go through entries written in this spec: the inline
// commands, and the commands written beside a $ref.
func findTarget(doc *cfgedit.YAMLDoc, into string, under []string, dir string) (mountTarget, error) {
	if doc.Root == nil || doc.Root.Kind != cfgedit.YAMLMapping || doc.Root.Flow {
		return mountTarget{}, fmt.Errorf("%s: not a spec: the document isn't a block mapping", into)
	}
	cmdPair := specKey(doc.Root, "command")
	if cmdPair == nil || cmdPair.Value.Kind != cfgedit.YAMLMapping || cmdPair.Value.Flow {
		return mountTarget{}, fmt.Errorf("%s: not a spec: it has no command: block", into)
	}
	t := mountTarget{cmd: cmdPair.Value, where: "the root command"}
	if p := specKey(t.cmd, "name"); p != nil {
		t.where = p.Value.Value
	}
	for _, name := range under {
		list, err := commandsOf(t, into)
		if err != nil && !errors.Is(err, errNoCommands) {
			return mountTarget{}, err
		}
		entries, err := mountedCommands(list, dir)
		if err != nil {
			return mountTarget{}, fmt.Errorf("%s: %w", into, err)
		}
		i := slices.IndexFunc(entries, func(c mountedCommand) bool { return slices.Contains(c.names, name) })
		if i < 0 {
			if t.abs != "" {
				return mountTarget{}, fmt.Errorf("%s: %s has no command %q written here; if it's declared in %s, mount into that spec instead", into, t.where, name, t.ref)
			}
			return mountTarget{}, fmt.Errorf("%s: %s has no command %q", into, t.where, name)
		}
		c := entries[i]
		if t.abs != "" && c.ref == "" {
			own, err := refCommandNames(t.abs)
			if err != nil {
				return mountTarget{}, fmt.Errorf("%s: %w", into, err)
			}
			if slices.Contains(own, name) {
				return mountTarget{}, fmt.Errorf("%s: %s %s overrides a command declared in %s; mount into that spec instead", into, t.where, name, t.ref)
			}
		}
		if c.item.Value.Kind != cfgedit.YAMLMapping || c.item.Value.Flow {
			return mountTarget{}, fmt.Errorf("%s: %s %s is written as a {...} mapping; write it as a block mapping to mount into it", into, t.where, name)
		}
		t = mountTarget{cmd: c.item.Value, path: append(slices.Clone(t.path), i), where: t.where + " " + name, ref: c.ref, abs: c.abs}
	}
	list, err := commandsOf(t, into)
	if err != nil && !errors.Is(err, errNoCommands) {
		return mountTarget{}, err
	}
	t.list = list
	return t, nil
}

// commandsOf returns t's commands list, nil when it has none, refusing one mount can't edit.
func commandsOf(t mountTarget, into string) (*cfgedit.YAMLPair, error) {
	list := specKey(t.cmd, "commands")
	if list == nil {
		return nil, errNoCommands
	}
	v := list.Value
	switch {
	case v.Empty, v.Kind == cfgedit.YAMLSequence && v.Flow && len(v.Items) == 0:
	case v.Kind != cfgedit.YAMLSequence:
		return nil, fmt.Errorf("%s: the commands of %s aren't a list", into, t.where)
	case v.Flow:
		return nil, fmt.Errorf("%s: the commands of %s are written as a [...] list; write it as a block list, one \"- \" entry per line, to mount into it", into, t.where)
	}
	return list, nil
}

// refCommandNames lists the names and aliases of the sub-commands a mounted spec declares
// itself, which commands written beside its $ref must not repeat. Its own $ref entries are read
// for their names; one that can't be read is left to validate.
func refCommandNames(abs string) ([]string, error) {
	child, err := readSpec(abs)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", abs, err)
	}
	var names []string
	for _, c := range child.Command.Commands {
		switch {
		case c.Name != "":
			names = append(names, c.Name)
		case c.Ref != "" && !strings.HasPrefix(c.Ref, modScheme) && !isExternalLocator(c.Ref):
			if s, err := readSpec(filepath.Join(filepath.Dir(abs), filepath.FromSlash(c.Ref))); err == nil {
				names = append(names, s.Command.Name)
				if len(c.Aliases) == 0 {
					names = append(names, s.Command.Aliases...)
				}
			}
		}
		names = append(names, c.Aliases...)
		names = append(names, c.HiddenAliases...)
	}
	return names, nil
}

// pluginTokens maps each plugin written on cmd to the name and aliases it dispatches as.
func pluginTokens(cmd *cfgedit.YAMLNode) map[string][]string {
	p := specKey(cmd, "plugins")
	if p == nil || p.Value.Kind != cfgedit.YAMLSequence {
		return nil
	}
	out := map[string][]string{}
	for _, item := range p.Value.Items {
		if item.Value.Kind != cfgedit.YAMLMapping {
			continue
		}
		name := specKey(item.Value, "name")
		if name == nil || name.Value.Kind != cfgedit.YAMLScalar {
			continue
		}
		tokens := []string{name.Value.Value}
		if a := specKey(item.Value, "aliases"); a != nil && a.Value.Kind == cfgedit.YAMLSequence {
			for _, v := range a.Value.Items {
				if v.Value.Kind == cfgedit.YAMLScalar {
					tokens = append(tokens, v.Value.Value)
				}
			}
		}
		out[name.Value.Value] = tokens
	}
	return out
}

func specKey(m *cfgedit.YAMLNode, key string) *cfgedit.YAMLPair {
	for _, p := range m.Pairs {
		if !p.Merge && p.Key.Value == key {
			return p
		}
	}
	return nil
}

// mountedCommands reads each entry of the commands list: its $ref, where that leads, and the
// names it takes. A local $ref is read for its name unless a name is written beside it.
func mountedCommands(list *cfgedit.YAMLPair, dir string) ([]mountedCommand, error) {
	if list == nil || list.Value.Kind != cfgedit.YAMLSequence {
		return nil, nil
	}
	var out []mountedCommand
	for _, item := range list.Value.Items {
		c := mountedCommand{item: item}
		if item.Value.Kind == cfgedit.YAMLMapping {
			if p := specKey(item.Value, "$ref"); p != nil && p.Value.Kind == cfgedit.YAMLScalar {
				c.ref = p.Value.Value
			}
			if p := specKey(item.Value, "name"); p != nil && p.Value.Kind == cfgedit.YAMLScalar {
				c.names = append(c.names, p.Value.Value)
			}
			for _, key := range []string{"aliases", "hidden_aliases"} {
				if p := specKey(item.Value, key); p != nil && p.Value.Kind == cfgedit.YAMLSequence {
					for _, a := range p.Value.Items {
						if a.Value.Kind == cfgedit.YAMLScalar {
							c.names = append(c.names, a.Value.Value)
						}
					}
				}
			}
		}
		if c.ref != "" && !strings.HasPrefix(c.ref, modScheme) && !isExternalLocator(c.ref) {
			c.abs = filepath.Clean(filepath.Join(dir, filepath.FromSlash(c.ref)))
			if err := c.readNames(); err != nil {
				return nil, err
			}
		}
		out = append(out, c)
	}
	return out, nil
}

// readNames fills in a local $ref's name and aliases from its spec, for the ones the entry
// doesn't write itself, and marks a missing file gone.
func (c *mountedCommand) readNames() error {
	if _, err := os.Stat(c.abs); errors.Is(err, iofs.ErrNotExist) {
		c.gone = true
		return nil
	}
	named := specKey(c.item.Value, "name") != nil
	aliased := specKey(c.item.Value, "aliases") != nil
	if named && aliased {
		return nil
	}
	child, err := readSpec(c.abs)
	if err != nil {
		return fmt.Errorf("read $ref %s: %w", c.ref, err)
	}
	if !named {
		c.names = append(c.names, child.Command.Name)
	}
	if !aliased {
		c.names = append(c.names, child.Command.Aliases...)
	}
	return nil
}

// mountEntry resolves a spec path given on the command line to the $ref the parent in dir
// writes for it, the spec's absolute path, and the command name and aliases it mounts as.
func mountEntry(spec, dir string) (MountEntry, string, []string, error) {
	if strings.HasPrefix(spec, modScheme) || isExternalLocator(spec) {
		return MountEntry{}, "", nil, fmt.Errorf("%s: name a spec file on disk; write a mod:// $ref by hand", spec)
	}
	abs, err := filepath.Abs(spec)
	if err != nil {
		return MountEntry{}, "", nil, fmt.Errorf("resolve %s: %w", spec, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return MountEntry{}, "", nil, fmt.Errorf("%s: %w", spec, err)
	}
	if info.IsDir() {
		return MountEntry{}, "", nil, fmt.Errorf("%s is a directory; name the spec file in it", spec)
	}
	rel, err := filepath.Rel(dir, abs)
	if err != nil {
		return MountEntry{}, "", nil, fmt.Errorf("%s: %w", spec, err)
	}
	child, err := readSpec(abs)
	if err != nil {
		return MountEntry{}, "", nil, fmt.Errorf("read %s: %w", spec, err)
	}
	names := append([]string{child.Command.Name}, child.Command.Aliases...)
	return MountEntry{Name: child.Command.Name, Ref: filepath.ToSlash(rel)}, abs, names, nil
}

// splice is one replacement of src[start:end].
type splice struct {
	start, end int
	text       string
}

// spliceMounts writes the new entries into the commands list and cuts the removed ones.
func spliceMounts(doc *cfgedit.YAMLDoc, cmdMap *cfgedit.YAMLNode, list *cfgedit.YAMLPair, existing, removed []mountedCommand, add []MountEntry, group string) []byte {
	src := doc.Src
	nl := "\n"
	if bytes.Contains(src, []byte("\r\n")) {
		nl = "\r\n"
	}
	var ops []splice
	kept := len(existing) - len(removed)
	for _, c := range removed {
		ops = append(ops, splice{commentsAbove(doc, c.item), doc.NextLine(c.item.Value.End), ""})
	}
	if kept == 0 && len(add) == 0 && list != nil {
		// Every entry went: drop the key too, rather than leave a commands: holding nothing.
		ops = []splice{{doc.LineStart(list.Key.Start), doc.NextLine(list.Value.End), ""}}
		return applySplices(src, ops)
	}

	if len(add) > 0 {
		var at, dash, body int
		var head string
		switch {
		case list == nil:
			at = doc.NextLine(cmdMap.End)
			dash = cmdMap.Col + 2
			body = dash + 2
			head = spaces(cmdMap.Col) + "commands:" + nl
		case len(existing) == 0:
			// commands: written empty, or as []: replace the value and write the list below it.
			ops = append(ops, splice{list.Colon + 1, list.Value.End, ""})
			at = doc.NextLine(list.Colon)
			dash = list.Col + 2
			body = dash + 2
		default:
			// After the last kept $ref entry, else the last kept entry, else where the last was.
			last := existing[len(existing)-1]
			var lastKept, lastRef *mountedCommand
			for i, c := range existing {
				if slices.ContainsFunc(removed, func(r mountedCommand) bool { return r.item == c.item }) {
					continue
				}
				lastKept = &existing[i]
				if c.ref != "" {
					lastRef = &existing[i]
				}
			}
			switch {
			case lastRef != nil:
				last = *lastRef
			case lastKept != nil:
				last = *lastKept
			}
			at = doc.NextLine(last.item.Value.End)
			dash = last.item.Col
			body = dash + 2
			if last.item.Value.Kind == cfgedit.YAMLMapping && !last.item.Value.Flow {
				body = last.item.Value.Col
			}
		}
		var b strings.Builder
		if at == len(src) && len(src) > 0 && src[len(src)-1] != '\n' {
			b.WriteString(nl)
		}
		b.WriteString(head)
		for _, e := range add {
			fmt.Fprintf(&b, "%s- %s$ref: %s%s", spaces(dash), spaces(body-dash-2), yamlText(e.Ref), nl)
			if group != "" {
				fmt.Fprintf(&b, "%sgroup: %s%s", spaces(body), yamlText(group), nl)
			}
		}
		ops = append(ops, splice{at, at, b.String()})
	}
	return applySplices(src, ops)
}

// commentsAbove returns the start of the comment lines attached to item, as fmt attaches them:
// those directly above its dash, indented no deeper than it. A blank line ends them, so a
// comment above one is a heading for what follows and stays; a deeper comment closes the entry
// before. Without attached comments it is the start of the dash's line.
func commentsAbove(doc *cfgedit.YAMLDoc, item *cfgedit.YAMLItem) int {
	start := doc.LineStart(item.Dash)
	for start > 0 {
		prev := doc.LineStart(start - 1)
		text := strings.TrimRight(string(doc.Src[prev:doc.LineEnd(prev)]), " \t\r")
		trimmed := strings.TrimLeft(text, " ")
		if !strings.HasPrefix(trimmed, "#") || len(text)-len(trimmed) > item.Col {
			break
		}
		start = prev
	}
	return start
}

// applySplices applies non-overlapping splices, last first so earlier offsets hold.
func applySplices(src []byte, ops []splice) []byte {
	slices.SortStableFunc(ops, func(a, b splice) int {
		if a.start != b.start {
			return b.start - a.start
		}
		return b.end - a.end
	})
	out := slices.Clone(src)
	for _, op := range ops {
		out = slices.Concat(out[:op.start], []byte(op.text), out[op.end:])
	}
	return out
}

// yamlText writes s as a plain scalar when it reads back as the same string, else quoted.
func yamlText(s string) string {
	if s != "" && !strings.ContainsAny(s, "\"'\\\t\r\n#:{}[],&*!|>%@`") && strings.TrimSpace(s) == s {
		var v map[string]any
		if yaml.Unmarshal([]byte("v: "+s+"\n"), &v) == nil {
			if got, ok := v["v"].(string); ok && got == s {
				return s
			}
		}
	}
	q := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(s)
	return `"` + q + `"`
}

// checkMounts decodes the edited spec and confirms its commands list is the old one, less the
// removed entries, plus the new ones after the last $ref, so a layout the splicing got wrong
// fails instead of being written.
func checkMounts(out []byte, into string, t mountTarget, existing, removed []mountedCommand, add []MountEntry) error {
	spec, err := decodeData[Spec](formatYAML, out, into)
	if err != nil {
		return fmt.Errorf("%s: the edited spec doesn't read back (nothing was written): %w", into, err)
	}
	var want []string
	lastRef := -1
	for _, c := range existing {
		if !slices.ContainsFunc(removed, func(r mountedCommand) bool { return r.item == c.item }) {
			want = append(want, c.ref)
			if c.ref != "" {
				lastRef = len(want) - 1
			}
		}
	}
	at := len(want)
	if lastRef >= 0 {
		at = lastRef + 1
	}
	var refs []string
	for _, e := range add {
		refs = append(refs, e.Ref)
	}
	want = slices.Insert(want, at, refs...)
	cmds := spec.Command.Commands
	for _, i := range t.path {
		if i >= len(cmds) {
			return fmt.Errorf("%s: the edited spec's commands don't come out as planned (nothing was written); add the $ref entries by hand", into)
		}
		cmds = cmds[i].Commands
	}
	var got []string
	for _, c := range cmds {
		got = append(got, c.Ref)
	}
	if !slices.Equal(got, want) {
		return fmt.Errorf("%s: the edited spec's commands don't come out as planned (nothing was written); add the $ref entries by hand", into)
	}
	return nil
}
