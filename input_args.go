package rotini

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/go-rotini/recon"
)

// channelArgument is the channel an [*InputError] about an argument's env or config fallback
// reports.
const channelArgument = "argument"

// commandArgs returns the Arguments sub-struct of a <Prefix>CommandInputs value, or an invalid
// Value when there is none.
func commandArgs(ci reflect.Value) reflect.Value {
	if ci.Kind() != reflect.Struct {
		return reflect.Value{}
	}
	a := ci.FieldByName("Arguments")
	if a.IsValid() && a.Kind() == reflect.Struct {
		return a
	}
	return reflect.Value{}
}

// reconcileArgs fills the leaf's arguments the command line left out from their env and config
// fallbacks, position by position: argv > env > config > default. Positionals fill left to right,
// so an argument's fallback applies only when every argument before it has a value; the first
// position with neither a fallback value nor a default ends the fill, as it ends the defaults.
// Each value is recorded in the store, with where it came from, so validation sees it, and the
// leaf's Arguments are bound again.
func reconcileArgs(reg *recon.Registry, v reflect.Value, chain []Command, store *parsedInputs, anchor int, rd fallbackRead) error {
	leaf := len(chain) - 1
	fi := leaf - anchor
	if store == nil || anchor < 0 || fi < 0 || fi >= v.NumField() || leaf >= len(store.scopes) {
		return nil
	}
	args := commandArgs(v.Field(fi))
	defs := chain[leaf].Arguments
	if !args.IsValid() || hasArgTail(defs) {
		return nil
	}
	si := &store.scopes[leaf]
	at := args.Type()
	n := min(len(defs), at.NumField())
	if !hasArgFallback(at, si.argvArgs, n) {
		return nil
	}
	clock := rd.view.clockRef()
	out := slices.Clone(si.args[:si.argvArgs])
fill:
	for i := si.argvArgs; i < n; i++ {
		ad := defs[i]
		vals, origin, err := argFallback(reg, args.Field(i), at.Field(i), ad, rd)
		if err != nil {
			return err
		}
		switch {
		case vals != nil:
			out = append(out, vals...)
			if si.argOrigin == nil {
				si.argOrigin = map[int]string{}
			}
			si.argOrigin[i] = origin
		case ad.Default != "":
			out = append(out, ad.Default)
		default:
			break fill // a gap: nothing after it can be placed
		}
	}
	si.args = out
	return bindArgs(args, si.args, defs, clock)
}

// hasArgFallback reports whether an argument from position from on declares a fallback.
func hasArgFallback(t reflect.Type, from, n int) bool {
	for i := from; i < n; i++ {
		if reconKey(t.Field(i).Tag.Get("recon")) != "" {
			return true
		}
	}
	return false
}

// argFallback reads one argument's fallback: its values as the command line would have given
// them (a variadic splits on its separator), checked against the field's type, and where they
// came from. nil when no source supplies one, or when a short-circuited run skips a
// configuration value the type cannot hold.
func argFallback(reg *recon.Registry, field reflect.Value, sf reflect.StructField, ad ArgDef, rd fallbackRead) (vals []string, origin string, err error) {
	key := reconKey(sf.Tag.Get("recon"))
	if key == "" || ad.Passthrough {
		return nil, "", nil
	}
	val, found, err := reg.Get(key)
	if err != nil {
		return nil, "", reconBind(channelArgument, err)
	}
	if !found {
		return nil, "", nil
	}
	source := val.Source()
	origin = fallbackOrigin(rd, source, sf.Tag.Get("env"), key)
	waivable := rd.waiveFiles && source != osEnvSourceName
	label := "<" + ad.Name + ">"
	if vals, err = fallbackValues(val, ad.Separator); err != nil {
		if waivable {
			return nil, "", nil
		}
		return nil, "", usageBind(channelArgument, ad.Name, fmt.Sprintf("%s: %v", label, err)+fromSource(origin), err)
	}
	if !ad.Variadic && len(vals) > 1 {
		vals = vals[len(vals)-1:] // one value, the last, as a scalar flag's fallback reads
	}
	vals = argEnum(ad).canonical(vals)
	probe := reflect.New(field.Type()).Elem()
	if err := coerceTime(probe, vals, argTimeSpec(ad, rd.view.clockRef())); err != nil {
		if waivable {
			return nil, "", nil
		}
		if mistake, ok := errors.AsType[authorMistake](err); ok {
			return nil, "", internalBind(channelArgument, ad.Name, fmt.Sprintf("%s: %s", label, mistake), err)
		}
		return nil, "", usageBind(channelArgument, ad.Name, fmt.Sprintf("%s: %s", label, coerceMessage(err, ad.Secret))+fromSource(origin), err)
	}
	if vals == nil {
		vals = []string{} // supplied, and empty: still present
	}
	return vals, origin, nil
}

// recordArgFallbacks is reconcileArgs for the per-channel layers ([Context.EnvInputs],
// [Context.FileInputs]): each argument whose fallback this layer supplies is bound, recorded as
// present, and noted by index for the merged report, which places positionals in order.
func recordArgFallbacks(set Presence, ci reflect.Value, frame Command, topName, layerName string, reg *recon.Registry, rd fallbackRead) (map[int]string, error) {
	var placed map[int]string
	args := commandArgs(ci)
	if !args.IsValid() || hasArgTail(frame.Arguments) {
		return placed, nil
	}
	at := args.Type()
	pos := 0
	for i := range min(len(frame.Arguments), at.NumField()) {
		ad := frame.Arguments[i]
		vals, _, err := argFallback(reg, args.Field(i), at.Field(i), ad, rd)
		if err != nil {
			return nil, err
		}
		if vals == nil {
			pos++
			continue
		}
		if err := coerceTime(args.Field(i), vals, argTimeSpec(ad, rd.view.clockRef())); err != nil {
			return nil, err
		}
		set[fieldPath(topName, "Arguments", at.Field(i).Name)] = InputSource{
			Layer: layerName,
			Raw:   redactValue(strings.Join(vals, ", "), ad.Secret),
		}
		if placed == nil {
			placed = map[int]string{}
		}
		for _, v := range vals {
			placed[pos] = v
			pos++
		}
	}
	return placed, nil
}
