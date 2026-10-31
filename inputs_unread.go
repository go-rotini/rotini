package rotini

import (
	"maps"
	"os"
	"slices"
	"strings"
)

// UnreadConfigKey is a key a configuration file holds that no input reads.
type UnreadConfigKey struct {
	File string // the config_files entry's name
	Path string // the file it was read from
	// Key is the key's dotted path from the file's top level; a profile's key starts with the
	// key holding the profiles and the profile's name (profiles.staging.replicaz).
	Key string
	// Line and Column are where the key's value sits, 1-indexed; both are 0 when the file's
	// format gives no positions.
	Line, Column int
}

// UnreadConfigKeys lists the keys of the configuration files this read opened that no input
// of the program reads, in the order the files were opened and each file's keys sorted. A key
// is read when an input of any command reads it ([ConfigFile.Keys]), and everything under it
// with it, so a map or object input's own keys are never listed. An `as: env` file, whose
// variables other programs may share, is left out, and so is a file no input of the command
// reads, which is never opened.
//
// Rotini says nothing about these keys by itself. A handler that wants a typo in a
// configuration file noticed warns about them, or a schema with `additionalProperties: false`
// makes them an error:
//
//	for _, k := range report.UnreadConfigKeys() {
//		fmt.Fprintf(rtx.Stderr, "warning: %s:%d: %s is not a setting\n", k.Path, k.Line, k.Key)
//	}
func (r InputReport) UnreadConfigKeys() []UnreadConfigKey {
	var out []UnreadConfigKey
	seen := map[[2]string]bool{}
	for _, d := range r.configDocs {
		id := [2]string{d.file.Name, d.src.Path()}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, unreadKeys(d)...)
	}
	return out
}

// unreadKeys lists the unread keys of one opened configuration file.
func unreadKeys(d openedDoc) []UnreadConfigKey {
	m, ok := d.src.Document()
	if !ok {
		return nil
	}
	read := map[string]bool{}
	under := map[string]bool{} // a proper prefix of a read key
	for _, k := range d.file.Keys {
		read[k.Key] = true
		for i := range len(k.Key) {
			if k.Key[i] == '.' {
				under[k.Key[:i]] = true
			}
		}
	}
	var paths [][]string
	var walk func(node map[string]any, rel, full []string)
	walk = func(node map[string]any, rel, full []string) {
		for _, k := range slices.Sorted(maps.Keys(node)) {
			r, f := append(slices.Clone(rel), k), append(slices.Clone(full), k)
			key := strings.Join(r, ".")
			switch child, isMap := node[k].(map[string]any); {
			case read[key]:
			case under[key]:
				if isMap {
					walk(child, r, f)
				}
			default:
				paths = append(paths, f)
			}
		}
	}
	if p := d.file.Profiles; p != nil {
		if profiles, ok := m[p.Under].(map[string]any); ok {
			for _, name := range slices.Sorted(maps.Keys(profiles)) {
				if section, ok := profiles[name].(map[string]any); ok {
					walk(section, nil, []string{p.Under, name})
				}
			}
		}
		delete(m, p.Under)
	}
	walk(m, nil, nil)
	slices.SortStableFunc(paths, func(a, b []string) int { return strings.Compare(strings.Join(a, "."), strings.Join(b, ".")) })
	if len(paths) == 0 {
		return nil
	}
	var data []byte
	if d.codec != nil && d.codec.locate != nil {
		if raw, err := os.ReadFile(d.src.Path()); err == nil {
			data = stripBOM(raw)
		}
	}
	out := make([]UnreadConfigKey, 0, len(paths))
	for _, p := range paths {
		k := UnreadConfigKey{File: d.file.Name, Path: d.src.Path(), Key: strings.Join(p, ".")}
		if data != nil {
			if line, col, ok := d.codec.locate(data, p); ok {
				k.Line, k.Column = line, col
			}
		}
		out = append(out, k)
	}
	return out
}
