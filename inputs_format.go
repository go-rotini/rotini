package rotini

import (
	"fmt"
	"io"
	"reflect"
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
// (envVars is a field's `env` tag, which may list several), else the configuration file's
// logical name and the key.
func valueOrigin(view *osView, source, envVars, key string) string {
	switch source {
	case "":
		return ""
	case osEnvSourceName:
		if envVars == "" {
			return "env"
		}
		return "env:" + chosenEnv(view, envVars)
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
	} else if _, leafSource, err := mapLeaves(reg, key); err == nil {
		source = leafSource
	}
	return valueOrigin(view, source, tag.Get("env"), key)
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
// layer's text included. A value with no text prints (document) for a decoded stdin document,
// (stream) for streamed stdin, which Format never reads, and (set) otherwise.
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
