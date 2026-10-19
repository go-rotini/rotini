package rotini

import (
	"encoding"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-rotini/recon"
)

// Messages about inputs follow one style: names the program declares appear bare (flags --x,
// variables APP_X, config keys a.b, command paths app group sub); tokens the user typed appear
// quoted (unknown flag "--nope", invalid value "xx"). An environment input is named by the
// variable the user sets, a configuration input by its key and the file the value came from.

// channelLabels names env and config inputs for messages: env holds each env input's declared
// variables by recon key, files each configuration source's file by its logical name.
type channelLabels struct {
	env   map[string][]string
	files map[string]string
	view  *osView
}

// envNames collects the variables each recon-keyed field of the structName sub-structs reads:
// its env tag's names when it has one (the first preferred), else the SNAKE_UPPER projection of
// its key under envPrefix, as the env source reads them.
func envNames(v reflect.Value, structName, envPrefix string) map[string][]string {
	out := map[string][]string{}
	if v.Kind() != reflect.Struct {
		return out
	}
	prefix := ""
	if envPrefix != "" {
		prefix = envPrefix + "_"
	}
	project := recon.SnakeUpperPrefixTransform(prefix)
	for _, ci := range v.Fields() {
		if ci.Kind() != reflect.Struct {
			continue
		}
		s := ci.FieldByName(structName)
		if !s.IsValid() || s.Kind() != reflect.Struct {
			continue
		}
		for f := range s.Type().Fields() {
			key := reconKey(f.Tag.Get("recon"))
			if key == "" || out[key] != nil {
				continue
			}
			if tag := f.Tag.Get("env"); tag != "" {
				for name := range strings.SplitSeq(tag, ",") {
					if name != "" {
						out[key] = append(out[key], name)
					}
				}
				continue
			}
			out[key] = []string{project(recon.ParsePath(key))}
		}
	}
	return out
}

// envLabel names an environment input: the variable that is set, else as [envUnsetLabel].
func envLabel(view *osView, names []string, key string) string {
	if set := firstSetEnv(view, strings.Join(names, ",")); set != "" {
		return "environment variable " + set + view.inputOrigin(set)
	}
	return envUnsetLabel(names, key)
}

// envUnsetLabel names an environment input none of whose variables is set: the preferred one,
// followed by the other spellings it accepts.
func envUnsetLabel(names []string, key string) string {
	switch len(names) {
	case 0:
		return "environment variable " + key
	case 1:
		return "environment variable " + names[0]
	}
	return fmt.Sprintf("environment variable %s (or %s)", names[0], strings.Join(names[1:], " or "))
}

// name labels the input at recon path on channel.
func (l channelLabels) name(channel, path string) string {
	switch channel {
	case channelEnv:
		return envLabel(l.view, l.env[path], path)
	case channelConfig:
		return "config key " + path
	}
	return fmt.Sprintf("%s %q", channelNoun(channel), path)
}

// origin is the " (from configuration file <path>)" suffix for a value a configuration source
// supplied, or "" when the source is not a known file.
func (l channelLabels) origin(source string) string {
	if p := l.files[source]; p != "" {
		if profile := sourceProfile(source); profile != "" {
			p += ", profile " + profile
		}
		return " (from configuration file " + p + ")"
	}
	return ""
}

// bind converts a recon error for one channel into a clean, categorized [*InputError], naming
// the input as the user sets it (see [reconBind]).
func (l channelLabels) bind(channel string, err error) error {
	if err == nil {
		return nil
	}
	label := func(path string) string {
		if path == "" {
			return channelDesc(channel)
		}
		return l.name(channel, path)
	}
	var ce *recon.CoercionError
	var mre *recon.MissingRequiredError
	var ve *recon.ValidationError
	var eve *recon.EmptyValueError
	switch {
	case errors.As(err, &ce):
		path, typ := ce.Path.String(), cleanType(ce.Target)
		return usageBind(channel, path,
			fmt.Sprintf("%s: expected %s%s", label(path), typ, l.origin(ce.Source)), err)
	case errors.As(err, &mre):
		return usageBind(channel, mre.Path.String(),
			fmt.Sprintf("%s is required", label(mre.Path.String())), err)
	case errors.As(err, &ve):
		return usageBind(channel, ve.Path.String(),
			fmt.Sprintf("%s: %s", label(ve.Path.String()), ve.Msg), err)
	case errors.As(err, &eve):
		return usageBind(channel, eve.Path.String(),
			fmt.Sprintf("%s must not be empty%s", label(eve.Path.String()), l.origin(eve.Source)), err)
	default:
		return usageBind(channel, "", fmt.Sprintf("could not read %s input", channelDesc(channel)), err)
	}
}

// bindStruct is [channelLabels.bind] for a bind of the Env or Config struct s from reg: a value
// its field's type cannot hold is reported as the command line reports one, with the value
// (redacted for a secret input), why it doesn't fit, and where it came from.
func (l channelLabels) bindStruct(channel string, err error, reg *recon.Registry, s reflect.Value) error {
	if ce, ok := asError[*recon.CoercionError](err); ok && reg != nil && l.view != nil {
		path := ce.Path.String()
		if msg, ok := l.valueProblem(reg, s, path, ce.Secret); ok {
			return usageBind(channel, path, l.name(channel, path)+": "+msg+l.origin(ce.Source), err)
		}
	}
	return l.bind(channel, err)
}

// valueProblem coerces the value reg holds at path into the type of the field of s that reads
// it, as a command-line value of that type, and words the failure. ok is false when there is no
// such field or value, the field's type is not a number, bool, time or duration (or a list of
// them), or the value coerces.
func (l channelLabels) valueProblem(reg *recon.Registry, s reflect.Value, path string, secret bool) (string, bool) {
	if s.Kind() != reflect.Struct {
		return "", false
	}
	for f := range s.Type().Fields() {
		if reconKey(f.Tag.Get("recon")) != path || !scalarShaped(f.Type) {
			continue
		}
		val, found, err := reg.Get(path)
		if err != nil || !found {
			return "", false
		}
		ts := tagTimeSpec(f.Tag)
		ts.clock = l.view.clockRef()
		cerr := coerceTime(reflect.New(f.Type).Elem(), channelTexts(val, f.Tag, f.Type), ts)
		if !errors.As(cerr, new(*coerceError)) {
			return "", false
		}
		return coerceMessage(cerr, secret), true
	}
	return "", false
}

// scalarShaped reports whether t, or the element of a list t, is a number, bool, time or
// duration that reads its text as the command line does.
func scalarShaped(t reflect.Type) bool {
	t = derefType(t)
	if t.Kind() == reflect.Slice && (t.Elem().Kind() != reflect.Uint8 || t.Name() == "") {
		t = derefType(t.Elem())
	}
	if t == timeType || t == durationType {
		return true
	}
	if reflect.PointerTo(t).Implements(reflect.TypeFor[encoding.TextUnmarshaler]()) {
		return false
	}
	switch t.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

// channelTexts is val as the text items a command-line value of type t would hold: a list's
// elements, or a string a list field reads split on its separator, else the one value.
func channelTexts(val recon.Value, tag reflect.StructTag, t reflect.Type) []string {
	if items, err := val.AsSlice(); err == nil && val.Kind() == recon.SliceKind {
		out := make([]string, len(items))
		for i, it := range items {
			out[i] = channelString(it)
		}
		return out
	}
	text := channelString(val)
	if derefType(t).Kind() == reflect.Slice {
		return listItems(tag, "[]", []string{text})
	}
	return []string{text}
}

// checkEnvMaps rejects an environment value for a map input that is not key=value pairs, as
// the flag form does: each comma-separated entry needs an "=" and a key.
func checkEnvMaps(s reflect.Value, reg *recon.Registry, l channelLabels) error {
	if s.Kind() != reflect.Struct || reg == nil {
		return nil
	}
	st := s.Type()
	for j := range s.NumField() {
		f := st.Field(j)
		key := reconKey(f.Tag.Get("recon"))
		if key == "" || f.Tag.Get("envnest") != "" || derefType(f.Type).Kind() != reflect.Map {
			continue
		}
		val, found, err := reg.Get(key)
		if err != nil || !found || val.Kind() != recon.StringKind {
			continue
		}
		secret := reconHasSecret(f.Tag.Get("recon"))
		label := l.name(channelEnv, key)
		for entry := range strings.SplitSeq(val.String(), ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			if msg := mapPairProblem(label, entry, secret); msg != "" {
				return usageBind(channelEnv, key, msg, nil)
			}
		}
	}
	return nil
}
