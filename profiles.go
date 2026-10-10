package rotini

import (
	"fmt"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"

	"github.com/go-rotini/recon"
)

// This file reads configuration files that hold named profiles (a config_files entry with
// `profiles`): which profile a run selects, and the two views of such a file the configuration
// registry reads, the selected profile's keys first and then the file's shared keys.

// profileChoice is the profile a run selects for one profiled configuration file.
type profileChoice struct {
	name     string // the selected profile; "" when nothing selects one
	input    string // the selector as the user set it: the flag's logical name, or the variable
	label    string // the selector in messages: "--profile", "ACME_PROFILE (from .env)"; "" for a default
	variable string // the variable that selected it; "" when argv or a default did
	explicit bool   // chosen on the command line or in the environment, not defaulted
}

// fileBootstrap is what the command line and the environment say about the configuration files
// before any of them is read: the paths config_source inputs supply and the selected profiles,
// each by the file's logical name.
type fileBootstrap struct {
	paths    map[string]string
	profiles map[string]profileChoice
}

// profileChoices selects a profile for each profiled configuration file in scope for chain.
func (b *InputReader) profileChoices(chain []Command, store *parsedInputs, view *osView) map[string]profileChoice {
	if !slices.ContainsFunc(b.configFiles, func(f ConfigFile) bool { return f.Profiles != nil }) {
		return nil
	}
	var out map[string]profileChoice
	for _, f := range b.chainFiles(chain, false) {
		if f.Profiles == nil {
			continue
		}
		if out == nil {
			out = map[string]profileChoice{}
		}
		out[f.Name] = selectProfile(f.Profiles, chain, store, view)
	}
	return out
}

// selectProfile is the profile p selects: the selector flag set on the command line, then the
// first of its variables that is set, then its default. Like config_source, the selection never
// reads a configuration file.
func selectProfile(p *ProfilesDef, chain []Command, store *parsedInputs, view *osView) profileChoice {
	if explicit, _ := storeFlagValue(store, p.Flag); explicit != "" {
		return profileChoice{name: explicit, input: p.Flag, label: typedFlagLabel(chain, store, p.Flag), explicit: true}
	}
	if name := firstSetEnv(view, p.Env); name != "" {
		return profileChoice{name: view.inputGetenv(name), input: name, label: name + view.inputOrigin(name), variable: name, explicit: true}
	}
	return profileChoice{name: p.Default}
}

// typedFlagLabel is the identifier a flag was last set through on the command line, else its
// canonical spelling.
func typedFlagLabel(chain []Command, store *parsedInputs, name string) string {
	label := name
	for i := range store.scopes {
		if !store.setOnArgv(i, name) {
			continue
		}
		label = store.scopes[i].typed[name]
		if label == "" && i < len(chain) {
			label = labelForFlag(chain[i].Flags, name)
		}
	}
	return label
}

// selectorFlags maps each selector flag in scope to the profile it selected.
func (boot fileBootstrap) selectorFlags(files []ConfigFile) map[string]profileChoice {
	var out map[string]profileChoice
	for _, f := range files {
		if f.Profiles == nil || f.Profiles.Flag == "" {
			continue
		}
		if _, seen := out[f.Profiles.Flag]; seen {
			continue
		}
		if out == nil {
			out = map[string]profileChoice{}
		}
		out[f.Profiles.Flag] = boot.profiles[f.Name]
	}
	return out
}

// applySelector writes the profile an environment variable selected into its selector flag, so
// the flag reports the profile in use. The selector is never read from a configuration file;
// argv and the flag's default were bound by the parser.
func applySelector(field reflect.Value, name string, chain []Command, store *parsedInputs, idx int, choice profileChoice, view *osView) error {
	if choice.variable == "" || store.setOnArgv(idx, name) {
		return nil
	}
	var def FlagDef
	if idx < len(chain) {
		def, _ = findFlagDef(chain[idx].Flags, name)
	}
	origin := "environment variable " + choice.label
	vals := flagEnum(def).canonical([]string{choice.name})
	if err := coerceFlagValues(field, def, vals, view.clockRef()); err != nil {
		return fallbackCoerceError(chain, idx, name, origin, err)
	}
	recordFlag(store, idx, name, vals)
	recordOrigin(store, idx, name, origin)
	return nil
}

// ── the two views of a profiled file ─────────────────────────────────────────────────.

// profileSourceName names the source answering for one profile of a file:
// "<file>#<under>.<profile>".
func profileSourceName(file, under, profile string) string {
	return file + "#" + under + "." + profile
}

// sourceProfile is the profile a configuration source answers for, "" for a file's own keys.
func sourceProfile(source string) string {
	_, rest, ok := strings.Cut(source, "#")
	if !ok {
		return ""
	}
	_, profile, _ := strings.Cut(rest, ".")
	return profile
}

// profileSource answers for one profile of a file: the keys under prefix ([under, profile]),
// read as if they sat at the top of the file.
type profileSource struct {
	src    recon.Source
	prefix recon.Path
}

func (s profileSource) Name() string { return s.src.Name() }

// Get reads p from the profile. A map the shared keys also hold is merged entry by entry, the
// profile's entries winning, so a map input reads both.
func (s profileSource) Get(p recon.Path) (recon.Value, bool, error) {
	v, found, err := s.src.Get(s.prefix.Append(p...))
	if err != nil || !found || v.Kind() != recon.MapKind || (len(p) > 0 && p[0] == s.prefix[0]) {
		return v, found, err //nolint:wrapcheck // a source's own result, which recon reports
	}
	shared, ok, err := s.src.Get(p)
	if err != nil || !ok {
		return v, true, nil //nolint:nilerr // the profile's own value stands when the shared one can't be read
	}
	return mergeValues(shared, v), true, nil
}

// mergeValues overlays over onto base: maps merge entry by entry, anything else is replaced.
func mergeValues(base, over recon.Value) recon.Value {
	bm, berr := base.AsMap()
	om, oerr := over.AsMap()
	if berr != nil || oerr != nil {
		return over
	}
	out := maps.Clone(bm)
	for k, v := range om {
		if prev, ok := out[k]; ok {
			v = mergeValues(prev, v)
		}
		out[k] = v
	}
	return recon.NewValue(out)
}

func (s profileSource) Keys() []recon.Path {
	var out []recon.Path
	for _, k := range s.src.Keys() {
		if len(k) > len(s.prefix) && k.HasPrefix(s.prefix) {
			out = append(out, k.After(s.prefix))
		}
	}
	return out
}

// Close does nothing: the file's shared view, read from the same source, closes it.
func (s profileSource) Close() error { return nil }

// sharedSource is a profiled file's keys outside its profiles, which every profile shares.
type sharedSource struct {
	recon.Source

	under string
}

func (s sharedSource) Get(p recon.Path) (recon.Value, bool, error) {
	if len(p) > 0 && p[0] == s.under {
		return recon.Value{}, false, nil
	}
	return s.Source.Get(p) //nolint:wrapcheck // a source's own result, which recon reports
}

func (s sharedSource) Keys() []recon.Path {
	var out []recon.Path
	for _, k := range s.Source.Keys() {
		if len(k) == 0 || k[0] != s.under {
			out = append(out, k)
		}
	}
	return out
}

// profiledSources is the sources a profiled file contributes, highest first: the selected
// profile's keys when the file defines it, then the shared keys.
func profiledSources(f ConfigFile, src recon.Source, path string, names []string, choice profileChoice) []recon.Source {
	under := f.Profiles.Under
	shared := namedSource{Source: sharedSource{Source: src, under: under}, name: f.Name, path: path}
	if choice.name == "" || !slices.Contains(names, choice.name) {
		return []recon.Source{shared}
	}
	profile := namedSource{
		Source: profileSource{src: src, prefix: recon.Path{under, choice.name}},
		name:   profileSourceName(f.Name, under, choice.name), path: path,
	}
	return []recon.Source{profile, shared}
}

// profileNames lists the profiles a file defines under under, sorted. A file without the key
// defines none; a key that does not map names to sections is a usage error.
func profileNames(f ConfigFile, src recon.Source, path string) ([]string, error) {
	under := f.Profiles.Under
	v, found, err := src.Get(recon.Path{under})
	if err != nil {
		return nil, usageBind(channelConfig, f.Name, configFileProblem(path, err), err)
	}
	if !found || v.Kind() == recon.NullKind {
		return nil, nil
	}
	bad := usageBind(channelConfig, f.Name, fmt.Sprintf("configuration file %s: %q must map profile names to sections", path, under), nil)
	m, err := v.AsMap()
	if err != nil {
		return nil, bad
	}
	for _, section := range m {
		if section.Kind() == recon.NullKind {
			continue // an empty profile
		}
		if _, err := section.AsMap(); err != nil {
			return nil, bad
		}
	}
	return slices.Sorted(maps.Keys(m)), nil
}

// effectiveDocument is what a profiled file's schema checks: the file's shared keys with the
// selected profile's merged over them key by key, without the profiles key itself.
func effectiveDocument(m map[string]any, under, profile string) map[string]any {
	out := maps.Clone(m)
	delete(out, under)
	sections, _ := m[under].(map[string]any)
	if section, ok := sections[profile].(map[string]any); ok && profile != "" {
		out = mergeLeaves(out, section)
	}
	return out
}

// mergeLeaves overlays over onto base: maps merge entry by entry, anything else is replaced.
func mergeLeaves(base, over map[string]any) map[string]any {
	out := maps.Clone(base)
	if out == nil {
		out = map[string]any{}
	}
	for k, v := range over {
		if om, ok := v.(map[string]any); ok {
			if bm, ok := out[k].(map[string]any); ok {
				out[k] = mergeLeaves(bm, om)
				continue
			}
		}
		out[k] = v
	}
	return out
}

// ── the unknown-profile check ─────────────────────────────────────────────────────────.

// checkProfiles reports a profile chosen on the command line or in the environment that no
// configuration file in scope with the same selector defines. A defaulted profile a file does
// not define leaves the shared keys, and a short-circuited run (waived) checks nothing, so a
// bad selection never blocks --help. Nor does an inputs struct v that reads no configuration
// file: a command that reads none never sees the error.
func (b *InputReader) checkProfiles(v reflect.Value, chain []Command, boot fileBootstrap, waived bool, view *osView) error {
	if waived || len(boot.profiles) == 0 || (!hasConfigChannel(v) && !hasReconFlags(v)) {
		return nil
	}
	type selection struct {
		choice profileChoice
		paths  []string // the files present
		names  []string // the profiles they define
		found  bool
	}
	var order []*selection
	bySelector := map[string]*selection{}
	for _, f := range b.chainFiles(chain, false) {
		choice := boot.profiles[f.Name]
		if f.Profiles == nil || !choice.explicit {
			continue
		}
		key := f.Profiles.Flag + "\x00" + f.Profiles.Env
		sel := bySelector[key]
		if sel == nil {
			sel = &selection{choice: choice}
			bySelector[key] = sel
			order = append(order, sel)
		}
		path, present, names, err := b.definedProfiles(f, boot.paths, view)
		if err != nil {
			return err
		}
		if present {
			sel.paths = append(sel.paths, path)
		}
		for _, n := range names {
			if !slices.Contains(sel.names, n) {
				sel.names = append(sel.names, n)
			}
		}
		sel.found = sel.found || slices.Contains(names, choice.name)
	}
	for _, sel := range order {
		if !sel.found {
			slices.Sort(sel.names)
			return unknownProfile(sel.choice, sel.paths, sel.names)
		}
	}
	return nil
}

// definedProfiles opens one profiled file and lists the profiles it defines, reporting whether
// the file exists at all.
func (b *InputReader) definedProfiles(f ConfigFile, paths map[string]string, view *osView) (path string, present bool, names []string, err error) {
	src, path, err := b.openFileSource(f, paths, view)
	if err != nil {
		return "", false, nil, err
	}
	defer src.Close()
	if path == "" {
		return "", false, nil, nil
	}
	if _, err := os.Stat(path); err != nil {
		return path, false, nil, nil // an absent optional file defines no profiles
	}
	names, err = profileNames(f, src, path)
	return path, true, names, err
}

// unknownProfile is the usage error for a selected profile no file defines. Its Token and
// Candidates are the name and the defined profiles, which [SuggestionFacts] reads.
func unknownProfile(choice profileChoice, paths, names []string) *InputError {
	msg := fmt.Sprintf("%s: profile %q is not defined", choice.label, choice.name)
	switch len(paths) {
	case 0:
		msg += ": no configuration file defines profiles"
	case 1:
		msg += " in configuration file " + paths[0]
	default:
		msg += " in configuration files " + strings.Join(paths, ", ")
	}
	if len(paths) > 0 {
		if len(names) == 0 {
			msg += " (it defines no profiles)"
		} else {
			msg += " (profiles: " + strings.Join(names, ", ") + ")"
		}
	}
	e := usageBind(channelConfig, choice.input, msg, nil)
	e.Token, e.Candidates = choice.name, names
	return e
}

// ── resolving one file for a run ───────────────────────────────────────────────────────.

// resolvedConfigFile is one config_files entry as the running command reads it: the file it
// resolves to, opened, and the profile the run selects.
type resolvedConfigFile struct {
	file    ConfigFile
	path    string // the file read; it may not exist. "" when a discover search has nowhere to look
	src     recon.Source
	profile profileChoice
}

// resolveConfigFile locates the named config_files entry the way the input reader does for the
// running command (its config_source path, its discover search, the run's environment, .env
// files and directory) and selects its profile from the command line and the environment. The
// caller closes src.
func resolveConfigFile(rtx *Context, name string) (resolvedConfigFile, error) {
	chain, err := layerChain(rtx)
	if err != nil {
		return resolvedConfigFile{}, err
	}
	b := readerFor(rtx)
	i := slices.IndexFunc(b.chainFiles(chain, false), func(f ConfigFile) bool { return f.Name == name })
	if i < 0 {
		return resolvedConfigFile{}, internalBind(channelConfig, name, fmt.Sprintf("no configuration file %q is in scope for this command", name), nil)
	}
	f := b.chainFiles(chain, false)[i]
	store, err := parseInto(chain, rtx.Argv, rtx.argvAcq())
	if err != nil {
		store = nil // a command line that cannot be parsed selects nothing
	}
	view := rtx.osView()
	paths := b.pathOverrides(chain, store, view)
	if layered, err := b.inputView(chain, reflect.Value{}, paths, true, view); err == nil && layered.hasInputEnv() {
		view = layered
		paths = b.pathOverrides(chain, store, view)
	}
	out := resolvedConfigFile{file: f}
	if f.Profiles != nil {
		out.profile = selectProfile(f.Profiles, chain, store, view)
	}
	if out.src, out.path, err = b.openFileSource(f, paths, view); err != nil {
		return resolvedConfigFile{}, err
	}
	return out, nil
}

// ConfigProfiles returns the names of the profiles the named config_files entry defines, sorted.
// It reads the file the running command would read: the path its config_source input gives, or
// its discover search, from the run's environment and directory. A file that does not exist
// defines none. It suits a completer for the profile selector:
//
//	names, err := rotini.ConfigProfiles(rtx, "app")
func ConfigProfiles(rtx *Context, file string) ([]string, error) {
	rf, err := resolveConfigFile(rtx, file)
	if err != nil {
		return nil, err
	}
	defer rf.src.Close()
	if rf.file.Profiles == nil {
		return nil, internalBind(channelConfig, file, fmt.Sprintf("configuration file %q declares no profiles", file), nil)
	}
	return profileNames(rf.file, rf.src, rf.path)
}
