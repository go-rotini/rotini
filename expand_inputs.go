package rotini

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/go-rotini/recon"
)

// This file applies declared path expansion (`expand`, `relative_to`) to the values each
// channel supplies, before they are checked and bound. The rule is read from the generated
// field's tags; see expandRule.

// parseArgvStore parses argv against the resolved chain into a store, not yet bound: the front
// half of [Parser.parseBind], which the input reader also calls so it can build its view of the
// environment before expanding and binding the values.
func parseArgvStore(p *Parser, rtx *Context, out any) (*parsedInputs, []Command, error) {
	if p == nil {
		return nil, nil, &ParseError{Kind: ParseKindInternal, Msg: "rotini: nil parser"}
	}
	if rtx == nil {
		return nil, nil, &ParseError{Kind: ParseKindInternal, Msg: "rotini: parse on nil context"}
	}
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return nil, nil, &ParseError{Kind: ParseKindInternal, Msg: "rotini: Parse out argument must be a non-nil pointer to an inputs struct"}
	}
	chain := rtx.CommandChain()
	if len(chain) == 0 {
		return nil, nil, &ParseError{Kind: ParseKindInternal, Msg: "rotini: no command resolved for this context"}
	}
	store, err := parseInto(chain, rtx.Argv, rtx.argvAcq())
	if err != nil {
		return nil, nil, err
	}
	return store, chain, nil
}

// expandStore expands the argv store's values (defaults included) for the flags and leaf
// arguments v declares with `expand`. An expansion failure is a usage error naming the input
// as typed.
func expandStore(v reflect.Value, chain []Command, store *parsedInputs, anchor int, view *osView) error {
	if store == nil || anchor < 0 || v.Kind() != reflect.Struct || anchor+v.NumField() > len(store.scopes) {
		return nil
	}
	var err error
	walkCommandStructs(v, chain, anchor, func(_ string, scope int, ci reflect.Value) {
		if err != nil {
			return
		}
		si := &store.scopes[scope]
		eachTaggedField(ci, "Flags", func(_, logical string, tag reflect.StructTag, _ reflect.Value) {
			r := tagExpandRule(tag)
			vals, ok := si.flags[logical]
			if err != nil || !r.expands() || !ok {
				return
			}
			label := flagErrLabel(*si, chain[scope].Flags, logical)
			out, xerr := expandValues(vals, r, view, label)
			if xerr != nil {
				err = argvExpandError(xerr, label)
				return
			}
			si.noteWritten(out, vals)
			si.flags[logical] = out
		})
		if err != nil || scope != len(chain)-1 {
			return
		}
		args := commandArgs(ci)
		if !args.IsValid() || len(si.args) == 0 {
			return
		}
		spans := argSpans(fieldArgDefs(args, chain[scope].Arguments), len(si.args))
		at := args.Type()
		for i := range args.NumField() {
			r := tagExpandRule(at.Field(i).Tag)
			s := spans[i]
			if !r.expands() || s[0] >= s[1] {
				continue
			}
			label := "<" + at.Field(i).Tag.Get("rotini") + ">"
			out, xerr := expandValues(si.args[s[0]:s[1]], r, view, label)
			if xerr != nil {
				err = argvExpandError(xerr, "")
				return
			}
			if !slices.Equal(out, si.args[s[0]:s[1]]) {
				si.noteWritten(out, si.args[s[0]:s[1]])
				si.args = slices.Clone(si.args)
				copy(si.args[s[0]:s[1]], out)
			}
		}
	})
	return err
}

// noteWritten records each value expansion changed against its written form, for [withWritten].
func (si *scopeInputs) noteWritten(expanded, written []string) {
	for i, x := range expanded {
		if i < len(written) && x != written[i] {
			if si.written == nil {
				si.written = map[string]string{}
			}
			si.written[x] = written[i]
		}
	}
}

// withWritten adds the value as written to a value error that names one of vals in its expanded
// form: `--file: no such file: "/home/ada/x" (written as "~/x")`. Any other error, or one about
// a value expansion left alone, is returned unchanged.
func withWritten(err error, vals []string, written map[string]string) error {
	pe, ok := err.(*ParseError) //nolint:errorlint // the value checks return a bare *ParseError
	if !ok || len(written) == 0 {
		return err
	}
	for _, v := range vals {
		if w, ok := written[v]; ok && strings.Contains(pe.Msg, strconv.Quote(v)) {
			cp := *pe
			cp.Msg += fmt.Sprintf(" (written as %q)", w)
			return &cp
		}
	}
	return err
}

// argvExpandError is an expansion failure on the command line, as the [*ParseError] any other
// bad argv value is.
func argvExpandError(err error, flag string) error {
	return &ParseError{Kind: ParseKindInvalidValue, Msg: err.Error(), Flag: flag}
}

// expandFallback expands a flag's or argument's env or config fallback values by its tags,
// then joins a relative value from a configuration file onto that file's directory when it
// declares `relative_to: config`. source is the recon source that supplied the values.
func expandFallback(vals []string, tag reflect.StructTag, source, label string, rd fallbackRead) ([]string, error) {
	r := tagExpandRule(tag)
	if !r.declared() {
		return vals, nil
	}
	out, err := expandValues(vals, r, rd.view, label)
	if err != nil {
		return nil, err
	}
	if r.relConfig && source != osEnvSourceName {
		out = relativeToFile(out, rd.files[source])
	}
	return out, nil
}

// expandChannels expands each command's Env and Config fields that declare `expand` or
// `relative_to`, after they are bound and before their values are checked.
func expandChannels(v reflect.Value, envReg *recon.Registry, cfg *cfgRegs, waived bool, labels channelLabels) error {
	if v.Kind() != reflect.Struct {
		return nil
	}
	for _, ci := range v.Fields() {
		if ci.Kind() != reflect.Struct {
			continue
		}
		if env := ci.FieldByName("Env"); envReg != nil && env.IsValid() {
			if _, err := expandChannelStruct(env, envReg, nil, waived, labels); err != nil {
				return err
			}
		}
		if conf := ci.FieldByName("Config"); cfg != nil && conf.IsValid() {
			if _, err := expandChannelStruct(conf, cfg.merged, cfg, waived, labels); err != nil {
				return err
			}
		}
	}
	return nil
}

// expandChannelStruct expands one Env or Config struct's declared fields in place: a string, or
// each element of a list, as bound. A field is expanded only when its registry supplied a value
// (a recon default included). It returns the expanded text of each field it changed, by recon
// key, for the presence report. In a short-circuited run (waived) a configuration value that
// cannot be expanded is left as written.
func expandChannelStruct(s reflect.Value, reg *recon.Registry, cfg *cfgRegs, waived bool, labels channelLabels) (map[string]string, error) {
	if s.Kind() != reflect.Struct || reg == nil {
		return nil, nil //nolint:nilnil // nothing to expand
	}
	var changed map[string]string
	st := s.Type()
	for j := range s.NumField() {
		sf := st.Field(j)
		r := tagExpandRule(sf.Tag)
		key := reconKey(sf.Tag.Get("recon"))
		if !r.declared() || key == "" {
			continue
		}
		fieldReg := reg
		if pin := sf.Tag.Get("cfgfile"); pin != "" && cfg != nil {
			var err error
			if fieldReg, err = cfg.For(pin); err != nil {
				return nil, err
			}
		}
		val, found, err := fieldReg.Get(key)
		if err != nil || !found {
			continue
		}
		vals, set := fieldStrings(s.Field(j))
		if set == nil {
			continue
		}
		channel, label := channelConfig, sf.Tag.Get("rotini")
		if cfg == nil {
			channel = channelEnv
			if env := sf.Tag.Get("env"); env != "" {
				label = chosenEnv(labels.view, env)
				label += labels.view.inputOrigin(label)
			}
		}
		out, xerr := expandValues(vals, r, labels.view, label)
		if xerr != nil {
			if waived && cfg != nil {
				continue
			}
			return nil, usageBind(channel, key, xerr.Error(), nil)
		}
		if r.relConfig && cfg != nil {
			out = relativeToFile(out, configSourceFile(cfg, val.Source()))
		}
		if slices.Equal(out, vals) {
			continue
		}
		set(out)
		if changed == nil {
			changed = map[string]string{}
		}
		changed[key] = strings.Join(out, ", ")
	}
	return changed, nil
}

// configSourceFile is the file a configuration source read, by its recon name; "" for a
// source that is no file (a custom source, a recon default).
func configSourceFile(cfg *cfgRegs, source string) string {
	if cfg == nil || source == "" {
		return ""
	}
	if p, ok := cfg.paths[source]; ok {
		return p
	}
	return ""
}

// fieldStrings reads a bound string-shaped field (string, []string, or a pointer to either) as
// its values, with a setter that writes new values back. The setter is nil for any other
// type, or a nil pointer, which supplies nothing.
func fieldStrings(f reflect.Value) ([]string, func([]string)) {
	f = indirectValue(f)
	switch {
	case !f.IsValid() || !f.CanSet():
		return nil, nil
	case f.Kind() == reflect.String:
		return []string{f.String()}, func(out []string) { f.SetString(out[0]) }
	case f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.String:
		vals := make([]string, f.Len())
		for i := range vals {
			vals[i] = f.Index(i).String()
		}
		return vals, func(out []string) {
			for i, v := range out {
				f.Index(i).SetString(v)
			}
		}
	}
	return nil, nil
}

// expandDefault expands a declared default by an input's tags, keeping it as written when it
// cannot be expanded; the run that reads it reports the failure.
func expandDefault(d string, tag reflect.StructTag, view *osView) string {
	r := tagExpandRule(tag)
	if !r.expands() || d == "" {
		return d
	}
	if out, err := expandValue(d, r, view); err == nil {
		return out
	}
	return d
}

// layerView is the view a per-channel layer expands values through: the run's own, with the
// .env files and variable_file values in scope layered in as [InputReader.Read] reads them.
// Files that cannot be read are skipped here; the env and files layers report them.
func layerView(rtx *Context, chain []Command, v reflect.Value, store *parsedInputs) *osView {
	view := rtx.osView()
	b := readerFor(rtx)
	if b == nil || !hasExpansion(v) {
		return view
	}
	overrides := map[string]string{}
	if store != nil {
		overrides = b.pathOverrides(chain, store, view)
	}
	if layered, err := b.inputView(chain, v, overrides, true, view); err == nil {
		return layered
	}
	return view
}

// hasExpansion reports whether any input v describes declares `expand`.
func hasExpansion(v reflect.Value) bool {
	if v.Kind() != reflect.Struct {
		return false
	}
	for _, ci := range v.Fields() {
		if ci.Kind() != reflect.Struct {
			continue
		}
		for _, name := range []string{"Flags", "Arguments", "Env", "Config"} {
			s := ci.FieldByName(name)
			if !s.IsValid() || s.Kind() != reflect.Struct {
				continue
			}
			for f := range s.Type().Fields() {
				if tagExpandRule(f.Tag).expands() {
					return true
				}
			}
		}
	}
	return false
}

// fallbackLabel is a flag's label in an error about its fallback value: its identifiers, as
// [fallbackCoerceError] names it.
func fallbackLabel(chain []Command, idx int, name string) string {
	if idx >= 0 && idx < len(chain) {
		return labelForFlag(chain[idx].Flags, name)
	}
	return name
}

// leafArgTags is the struct tag of each of the leaf's argument fields, by position; empty
// when v does not describe the leaf.
func leafArgTags(v reflect.Value, chain []Command, anchor int) []reflect.StructTag {
	tags := make([]reflect.StructTag, len(chain[len(chain)-1].Arguments))
	fi := len(chain) - 1 - anchor
	if v.Kind() != reflect.Struct || fi < 0 || fi >= v.NumField() {
		return tags
	}
	args := commandArgs(v.Field(fi))
	if !args.IsValid() {
		return tags
	}
	for i := range min(len(tags), args.NumField()) {
		tags[i] = args.Type().Field(i).Tag
	}
	return tags
}

// channelFieldType is the type an env or config field's values are checked as: its Go type's
// ([channelGoType]), except that a `path:"file"` or `path:"dir"` tag makes a string field an
// existingfile or existingdir, so the path is checked as a flag's is.
func channelFieldType(tag reflect.StructTag, t reflect.Type) string {
	typ := channelGoType(t)
	var kind string
	switch tag.Get("path") {
	case "file":
		kind = "existingfile"
	case "dir":
		kind = "existingdir"
	default:
		return typ
	}
	switch typ {
	case "string":
		return kind
	case "[]string":
		return "[]" + kind
	}
	return typ
}

// expandedStrings is a string-shaped field's bound values, which expansion may have rewritten,
// or fallback for any other field.
func expandedStrings(f reflect.Value, fallback []string) []string {
	if vals, set := fieldStrings(f); set != nil {
		return vals
	}
	return fallback
}
