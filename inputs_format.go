package rotini

import (
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/go-rotini/recon"
)

// redactedValue is what a secret input's value prints as.
const redactedValue = "[redacted]"

// Origins name where in its layer a value came from; see [InputSource.Origin].

// argvFlagOrigin is "argv:" and the identifier the flag was typed as.
func argvFlagOrigin(si scopeInputs, logical string) string {
	if typed := si.typed[logical]; typed != "" {
		return "argv:" + typed
	}
	return "argv"
}

// valueOrigin names a value recon read: from the environment, the variable that was set
// (envVars is a field's `env` tag, which may list several) or the variable_file variable whose
// file it read, or for a variable a .env file set, that file's logical name and the variable;
// else the configuration file's logical name and the key.
func valueOrigin(view *osView, source, envVars, key string) string {
	switch source {
	case "":
		return ""
	case osEnvSourceName:
		if envVars == "" {
			return "env"
		}
		name := chosenEnv(view, envVars)
		if file := view.fileVariable(name); file != "" {
			return "env:" + file
		}
		if dotenv := view.dotenvSource(name); dotenv != "" {
			return "config:" + dotenv + "#" + name
		}
		return "env:" + name
	}
	if strings.Contains(source, "#") {
		return "config:" + source + "." + key // a profile's source: config:app#profiles.prod.region
	}
	return "config:" + source + "#" + key
}

// fallbackValueOrigin is valueOrigin for a flag's or argument's env or config fallback at key,
// read again from reg; a map's value comes from its leaves.
func fallbackValueOrigin(reg *recon.Registry, view *osView, tag reflect.StructTag, key string) string {
	if reg == nil || key == "" {
		return ""
	}
	source := ""
	if val, found, err := reg.Get(key); err == nil && found {
		source = val.Source()
	} else if _, leafSource, err := mapLeaves(fallbackLookup{reg: reg}, key); err == nil {
		source = leafSource
	}
	return valueOrigin(view, source, tag.Get("env"), key)
}

// rawText is a value recon read as [InputSource.Raw] gives it: a list's items joined with ", ",
// a map's entries as sorted key=value pairs (nested maps as dotted keys), anything else as its
// text.
func rawText(val recon.Value) string {
	switch val.Kind() {
	case recon.SliceKind:
		if items, err := val.AsSlice(); err == nil {
			texts := make([]string, len(items))
			for i, it := range items {
				texts[i] = valueText(it)
			}
			return strings.Join(texts, ", ")
		}
	case recon.MapKind:
		if m, err := val.AsMap(); err == nil {
			var entries []string
			flattenMap("", m, &entries)
			slices.Sort(entries)
			return strings.Join(entries, ", ")
		}
	}
	return val.String()
}

// nestedEnvOrigin names a nested env family by its variables' shared prefix: "env:APP_HTTP__*".
func nestedEnvOrigin(tag reflect.StructTag) string {
	base, rest, _ := strings.Cut(tag.Get("envnest"), ",")
	sep, _, _ := strings.Cut(rest, ",")
	return "env:" + base + sep + "*"
}

// Format writes one line per field a layer set, sorted by field path: the field, its value
// (redacted when the input is secret), where it came from ([InputSource.Origin], else the
// layer's name), and the values it overrode, nearest first. It writes nothing for a report with
// no fields, and returns the first write error.
//
//	TaskrDeploy.Flags.Port = "9000" from env:TASKR_PORT; overrides config:user#deploy.port "8000", default "8080"
//	TaskrDeploy.Flags.Token = [redacted] from argv:--token
//	TaskrDeploy.Arguments.Target = "prod" from argv:<target>
//	TaskrDeploy.Stdin = (document) from stdin
//
// A field the spec marks secret prints [redacted] whatever a layer supplied, a hand-built
// layer's text included. A value with no text prints (stream) for streamed stdin, which Format
// never reads, (document) for a decoded stdin document, "" for an empty string, and (set)
// otherwise.
func (r InputReport) Format(w io.Writer) error {
	paths := r.Fields()
	if len(paths) == 0 {
		return nil
	}
	secret := r.secretFields()
	var b strings.Builder
	for _, path := range paths {
		history := r.history[path]
		if len(history) == 0 {
			continue
		}
		win := history[len(history)-1]
		b.WriteString(string(path))
		b.WriteString(" = ")
		b.WriteString(r.formatValue(path, win, secret[path]))
		b.WriteString(" from ")
		b.WriteString(sourceName(win))
		for i := len(history) - 2; i >= 0; i-- {
			if i == len(history)-2 {
				b.WriteString("; overrides ")
			} else {
				b.WriteString(", ")
			}
			b.WriteString(sourceName(history[i]))
			b.WriteString(" ")
			b.WriteString(r.formatValue(path, history[i], secret[path]))
		}
		b.WriteString("\n")
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write the input report: %w", err)
	}
	return nil
}

// sourceName is where a value came from: its origin, else its layer.
func sourceName(s InputSource) string {
	if s.Origin != "" {
		return s.Origin
	}
	return s.Layer
}

// formatValue renders one supplied value for Format.
func (r InputReport) formatValue(path FieldPath, s InputSource, secret bool) string {
	switch {
	case secret || s.Raw == redactedValue:
		return redactedValue
	case s.Raw != "":
		return strconv.Quote(s.Raw)
	}
	field := r.mergedField(path)
	switch {
	case field.IsValid() && field.Kind() == reflect.Func:
		return "(stream)"
	case strings.HasSuffix(string(path), ".Stdin"):
		return "(document)"
	case field.IsValid() && field.Kind() == reflect.String:
		return `""`
	}
	return "(set)"
}

// mergedField is the merged value's field at path, or the zero Value.
func (r InputReport) mergedField(path FieldPath) reflect.Value {
	v := r.merged
	for seg := range strings.SplitSeq(string(path), ".") {
		if !v.IsValid() || v.Kind() != reflect.Struct {
			return reflect.Value{}
		}
		v = v.FieldByName(seg)
	}
	return v
}

// secretFields lists the fields of the merged value the spec marks secret: flags and arguments
// by their definitions on the chain, env and config inputs by their recon tags.
func (r InputReport) secretFields() map[FieldPath]bool {
	out := map[FieldPath]bool{}
	if !r.merged.IsValid() || len(r.chain) == 0 {
		return out
	}
	walkCommandStructs(r.merged, r.chain, r.anchor, func(topName string, scope int, ci reflect.Value) {
		frame := r.chain[scope]
		eachTaggedField(ci, "Flags", func(fieldName, logical string, _ reflect.StructTag, _ reflect.Value) {
			if fd, ok := findFlagDef(frame.Flags, logical); ok && fd.Secret {
				out[fieldPath(topName, "Flags", fieldName)] = true
			}
		})
		args := ci.FieldByName("Arguments")
		if args.IsValid() && args.Kind() == reflect.Struct {
			for j := range min(args.NumField(), len(frame.Arguments)) {
				if frame.Arguments[j].Secret {
					out[fieldPath(topName, "Arguments", args.Type().Field(j).Name)] = true
				}
			}
		}
		for _, channel := range []string{"Env", "Config"} {
			eachTaggedField(ci, channel, func(fieldName, _ string, tag reflect.StructTag, _ reflect.Value) {
				if reconHasSecret(tag.Get("recon")) {
					out[fieldPath(topName, channel, fieldName)] = true
				}
			})
		}
	})
	return out
}

// inputNames is what a report needs to name inputs and files as the user knows them: the env
// prefix derived variable names use, the configuration files in scope for the chain (nearest
// first), and the file each was read from, by logical name, once the files channel opened it.
type inputNames struct {
	envPrefix string
	files     []ConfigFile
	paths     map[string]string
	dir       string // the run's directory, which a file inside is named relative to
}

// namesFor is the reader's inputNames for chain, with the files read so far.
func (b *InputReader) namesFor(chain []Command, paths map[string]string, view *osView) *inputNames {
	n := &inputNames{envPrefix: b.envPrefix, files: b.chainConfigFiles(chain), paths: paths}
	if dir, err := view.getwd(); err == nil {
		n.dir = dir // with no directory, a file keeps the path it was read from
	}
	return n
}

// merge is n with other's facts added: the first prefix and file list, and every path read.
func (n *inputNames) merge(other *inputNames) *inputNames {
	switch {
	case other == nil:
		return n
	case n == nil:
		return other
	}
	out := *n
	if len(other.paths) > 0 {
		out.paths = maps.Clone(n.paths)
		if out.paths == nil {
			out.paths = map[string]string{}
		}
		maps.Copy(out.paths, other.paths)
	}
	return &out
}

// file is the configuration file with logical name, or nil.
func (n *inputNames) file(name string) *ConfigFile {
	if n == nil {
		return nil
	}
	for i := range n.files {
		if n.files[i].Name == name {
			return &n.files[i]
		}
	}
	return nil
}

// fileLabel names the file with logical name as the user knows it: the path it was read from,
// else its declared path, else the file name a search looks for.
func (n *inputNames) fileLabel(name string) string {
	if n == nil {
		return name
	}
	if p := n.paths[name]; p != "" {
		return n.short(p)
	}
	for source, p := range n.paths {
		if strings.HasPrefix(source, name+"#") {
			return n.short(p) // a profiled file: every profile's source reads the one file
		}
	}
	if f := n.file(name); f != nil {
		switch {
		case f.Path != "":
			return f.Path
		case f.Discover != nil:
			return f.Discover.File
		}
	}
	return name
}

// short is path relative to the run's directory when it lies inside it, else path.
func (n *inputNames) short(path string) string {
	if n.dir == "" || !filepath.IsAbs(path) {
		return path
	}
	if rel, err := filepath.Rel(n.dir, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rel
	}
	return path
}

// readsConfig reports whether a configuration file (not a .env file) is in scope.
func (n *inputNames) readsConfig() bool {
	return n != nil && slices.ContainsFunc(n.files, func(f ConfigFile) bool { return f.As != "env" })
}

// reportInput is one input of a report's inputs type, as its definitions and tags declare it.
type reportInput struct {
	channel string // "Flags", "Arguments", "Env", "Config" or "Stdin"
	tag     reflect.StructTag
	flag    FlagDef
	arg     ArgDef
}

// input finds the declared input at path, or reports false when the report has no rotini layer
// or path names no input.
func (r InputReport) input(path FieldPath) (reportInput, bool) {
	top, rest, _ := strings.Cut(string(path), ".")
	channel, field, _ := strings.Cut(rest, ".")
	var in reportInput
	found := false
	if !r.merged.IsValid() || len(r.chain) == 0 {
		return in, false
	}
	walkCommandStructs(r.merged, r.chain, r.anchor, func(topName string, scope int, ci reflect.Value) {
		if found || topName != top {
			return
		}
		frame := r.chain[scope]
		switch channel {
		case "Stdin":
			in, found = reportInput{channel: channel}, field == "" && ci.FieldByName("Stdin").IsValid()
		case "Flags", "Env", "Config":
			eachTaggedField(ci, channel, func(fieldName, logical string, tag reflect.StructTag, _ reflect.Value) {
				if found || fieldName != field {
					return
				}
				in, found = reportInput{channel: channel, tag: tag}, true
				if channel == "Flags" {
					in.flag, _ = findFlagDef(frame.Flags, logical)
					if in.flag.Name == "" {
						in.flag.Name = logical
					}
				}
			})
		case "Arguments":
			args := ci.FieldByName("Arguments")
			if !args.IsValid() || args.Kind() != reflect.Struct {
				return
			}
			for j := range args.NumField() {
				sf := args.Type().Field(j)
				if sf.Name != field {
					continue
				}
				in, found = reportInput{channel: channel, tag: sf.Tag}, true
				if j < len(frame.Arguments) {
					in.arg = frame.Arguments[j]
				} else {
					in.arg.Name = sf.Tag.Get("rotini")
				}
			}
		}
	})
	return in, found
}

// Label returns the user's name for the input at path: a flag's first long identifier
// (--replicas, else its first identifier), an argument as <name>, an env input's first
// variable (CFGCTL_REGION, or CFGCTL_HTTP__* for a nested family), a configuration input's
// key (deploy.timeout), and stdin for the stdin payload. It returns "" when path names no input
// or the report has no layer rotini read, which says what the spec declares.
//
//	report.Label("CfgctlDeploy.Flags.Replicas") // "--replicas"
func (r InputReport) Label(path FieldPath) string {
	in, ok := r.input(path)
	if !ok {
		return ""
	}
	switch in.channel {
	case "Flags":
		return flagLabel(in.flag)
	case "Arguments":
		return "<" + in.arg.Name + ">"
	case "Env":
		if strings.Contains(in.tag.Get("envnest"), ",") {
			return strings.TrimPrefix(nestedEnvOrigin(in.tag), "env:")
		}
		if vars := r.envVars(in.tag); len(vars) > 0 {
			return vars[0]
		}
	case "Config":
		return reconKey(in.tag.Get("recon"))
	case "Stdin":
		return "stdin"
	}
	return ""
}

// envVars is the variables a field reads, in lookup order: its env tag's names, else the
// SNAKE_UPPER projection of its recon key under the env prefix; nil when it reads none.
func (r InputReport) envVars(tag reflect.StructTag) []string {
	var out []string
	for name := range strings.SplitSeq(tag.Get("env"), ",") {
		if name != "" {
			out = append(out, name)
		}
	}
	if len(out) > 0 {
		return out
	}
	key := reconKey(tag.Get("recon"))
	if key == "" {
		return nil
	}
	prefix := ""
	if r.names != nil && r.names.envPrefix != "" {
		prefix = r.names.envPrefix + "_"
	}
	return []string{recon.SnakeUpperPrefixTransform(prefix)(recon.ParsePath(key))}
}

// Sources returns every place the spec declares for the input at path to take its value from,
// highest precedence first, in the user's words: the flag or <argument>, its environment
// variables, the file a variable_file variable names, its configuration key (when a
// configuration file is in scope; "<key> in <file>" for an input pinned to one file), and
// default when it declares one. It returns nil when [InputReport.Label] would return "".
//
//	report.Sources("CfgctlDeploy.Flags.Replicas")
//	// ["--replicas", "CFGCTL_DEPLOY_REPLICAS", "deploy.replicas", "default"]
func (r InputReport) Sources(path FieldPath) []string {
	in, ok := r.input(path)
	if !ok {
		return nil
	}
	label := r.Label(path)
	recTag := in.tag.Get("recon")
	key := reconKey(recTag)
	var out []string
	switch in.channel {
	case "Stdin":
		return []string{label}
	case "Flags", "Arguments":
		out = append(out, label)
		if key != "" || in.tag.Get("env") != "" {
			out = append(out, r.envVars(in.tag)...)
		}
	case "Env":
		if strings.Contains(in.tag.Get("envnest"), ",") {
			out = append(out, label)
		} else {
			out = append(out, r.envVars(in.tag)...)
		}
	}
	if file := in.tag.Get("envfile"); file != "" {
		out = append(out, "the file "+file+" names")
	}
	if key != "" && in.channel != "Env" {
		if pin := in.tag.Get("cfgfile"); pin != "" {
			out = append(out, key+" in "+r.names.fileLabel(pin))
		} else if r.names.readsConfig() {
			out = append(out, key)
		}
	}
	var declared bool
	switch in.channel {
	case "Flags":
		declared = in.flag.Default != "" || len(in.flag.Defaults) > 0
	case "Arguments":
		declared = in.arg.Default != ""
	default:
		declared = reconDefault(recTag) != ""
	}
	if declared {
		out = append(out, "default")
	}
	return out
}

// Explain is [InputReport.Format] in the user's words: one line per field a layer set, sorted
// as Format sorts them, naming the input by [InputReport.Label] and each source as the user
// sets it. A field the report can't name keeps its field path.
//
//	--port = "9000" from TODO_PORT (overrides "8000" from deploy.port in todo.yaml, "8080" from default)
//	--token = [redacted] from the file TODO_TOKEN_FILE names
//	<target> = "prod" from the command line
//	deploy.timeout = "5m" from default
//
// A source reads as the command line, a variable (CFGCTL_HTTP__* for a nested family), the
// file a variable_file variable names, "<key> in <file>" (with "(profile prod)" for a key of
// the selected profile, and the .env file for a variable one set), default or stdin; a
// hand-built layer's value is named by its layer. Values print as Format prints them.
func (r InputReport) Explain(w io.Writer) error {
	paths := r.Fields()
	if len(paths) == 0 {
		return nil
	}
	secret := r.secretFields()
	var b strings.Builder
	for _, path := range paths {
		history := r.history[path]
		if len(history) == 0 {
			continue
		}
		in, _ := r.input(path)
		label := r.Label(path)
		if label == "" {
			label = string(path)
		}
		win := history[len(history)-1]
		b.WriteString(label)
		b.WriteString(" = ")
		b.WriteString(r.formatValue(path, win, secret[path]))
		b.WriteString(" from ")
		b.WriteString(r.userSource(win, in))
		for i := len(history) - 2; i >= 0; i-- {
			if i == len(history)-2 {
				b.WriteString(" (overrides ")
			} else {
				b.WriteString(", ")
			}
			b.WriteString(r.formatValue(path, history[i], secret[path]))
			b.WriteString(" from ")
			b.WriteString(r.userSource(history[i], in))
		}
		if len(history) > 1 {
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write the input report: %w", err)
	}
	return nil
}

// userSource words where s's value came from for [InputReport.Explain]. A map merged from
// several places names each, comma-separated.
func (r InputReport) userSource(s InputSource, in reportInput) string {
	if s.Origin == "" {
		return s.Layer
	}
	parts := strings.Split(s.Origin, ", ")
	for i, o := range parts {
		parts[i] = r.userOrigin(o, in)
	}
	return strings.Join(parts, ", ")
}

// userOrigin words one origin; see [InputSource.Origin].
func (r InputReport) userOrigin(origin string, in reportInput) string {
	kind, rest, _ := strings.Cut(origin, ":")
	switch kind {
	case "argv":
		return "the command line"
	case "env":
		if rest == "" {
			return "the environment"
		}
		if rest == in.tag.Get("envfile") {
			return "the file " + rest + " names"
		}
		return rest
	case "config":
		name, key, ok := strings.Cut(rest, "#")
		if !ok {
			return origin
		}
		file := r.names.fileLabel(name)
		if f := r.names.file(name); f != nil && f.Profiles != nil {
			if after, ok := strings.CutPrefix(key, f.Profiles.Under+"."); ok {
				if profile, k, ok := strings.Cut(after, "."); ok {
					return k + " in " + file + " (profile " + profile + ")"
				}
			}
		}
		return key + " in " + file
	}
	return origin
}
