package rotini

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// A flag's or argument's fallback is read straight from the declared environment when the
// environment is its only source and its keys are plain, and through a recon registry
// otherwise. The conformance suites run every case both ways and compare everything a read
// shows: the values bound, the errors, where each value came from, and the InputReport with
// its --explain-inputs text.

// fallbackMode is the path the running conformance case keeps: "direct" or "registry".
var fallbackMode = "direct"

// fallbackPaths runs fn twice. "direct" keeps the direct environment lookup wherever it
// applies; "registry" sends every fallback read through a registry. In both, each fallback pass
// also runs the other way, on its own copy of the inputs and the parsed command line, and any
// difference in the values bound, the origins recorded or the error fails the test.
//
// A pass whose reader has configuration files or custom sources always reads through a
// registry, so in "direct" it also runs both ways once more without them, on copies, and the
// two must agree. That pass's result is not kept: the case still sees the reader it declared.
//
// It replaces package state, so fn must not use t.Parallel outside its own subtests.
func fallbackPaths(t *testing.T, fn func(t *testing.T)) {
	t.Helper()
	for _, mode := range []string{"direct", "registry"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			var diffs []string
			prevMode, prevReconcile := fallbackMode, wiredFallbackHooks.reconcile
			fallbackMode = mode
			// both runs one fallback pass over copies of v and store, chosen and through a registry,
			// and records any difference under label.
			both := func(label string, b *InputReader, v reflect.Value, chain []Command, store *parsedInputs, boot fileBootstrap, anchor int, waived bool, view *osView) {
				v, store = cloneInputs(v), cloneStore(store)
				other, otherStore := cloneInputs(v), cloneStore(store)
				var err, otherErr error
				countPath(v.Type(), func() { err = prevReconcile(b, v, chain, store, boot, anchor, waived, view) })
				viaRegistry(v.Type(), true, func() {
					otherErr = prevReconcile(b, other, chain, otherStore, boot, anchor, waived, view)
				})
				if d := fallbackDiff(v, other, store, otherStore, err, otherErr); d != "" {
					mu.Lock()
					diffs = append(diffs, fmt.Sprintf("%s %v %s: %s", label, chainNames(chain), v.Type(), d))
					mu.Unlock()
				}
			}
			wiredFallbackHooks.reconcile = func(b *InputReader, v reflect.Value, chain []Command, store *parsedInputs, boot fileBootstrap, anchor int, waived bool, view *osView) error {
				if bare := envOnlyReader(b); bare != nil && mode == "direct" {
					both("without configuration files:", bare, v, chain, store, boot, anchor, waived, view)
				}
				other, otherStore := cloneInputs(v), cloneStore(store)
				var err, otherErr error
				run := func() { err = prevReconcile(b, v, chain, store, boot, anchor, waived, view) }
				if mode == "direct" {
					countPath(v.Type(), run)
				} else {
					viaRegistry(v.Type(), true, run)
				}
				viaRegistry(v.Type(), mode == "direct", func() {
					otherErr = prevReconcile(b, other, chain, otherStore, boot, anchor, waived, view)
				})
				if d := fallbackDiff(v, other, store, otherStore, err, otherErr); d != "" {
					mu.Lock()
					diffs = append(diffs, fmt.Sprintf("%v %s: %s", chainNames(chain), v.Type(), d))
					mu.Unlock()
				}
				return err
			}
			defer func() { fallbackMode, wiredFallbackHooks.reconcile = prevMode, prevReconcile }()
			fn(t)
			for _, d := range diffs {
				t.Errorf("direct lookup and registry differ: %s", d)
			}
		})
	}
}

// envOnlyReader is b without its configuration files and custom sources, keeping the .env
// files read as environment, or nil when b has neither.
func envOnlyReader(b *InputReader) *InputReader {
	files := envOnlyFiles(b.configFiles)
	if len(files) == len(b.configFiles) && len(b.sources) == 0 {
		return nil
	}
	bare := *b
	bare.configFiles, bare.sources = files, nil
	return &bare
}

// envOnlySettings is meta without its configuration files and custom sources, keeping the .env
// files read as environment.
func envOnlySettings(meta InputSettings) InputSettings {
	meta.ConfigFiles, meta.Sources = envOnlyFiles(meta.ConfigFiles), nil
	return meta
}

func envOnlyFiles(files []ConfigFile) []ConfigFile {
	return slices.DeleteFunc(slices.Clone(files), func(f ConfigFile) bool { return f.As != "env" })
}

// fallbackPathCounts counts the chosen fallback passes of the "direct" runs by the path each
// took.
var fallbackPathCounts struct{ direct, registry atomic.Int64 }

// subtestsFiltered reports whether -run or -skip may leave out some subtests, so a count over a
// whole suite does not hold.
func subtestsFiltered() bool {
	run, skip := flag.Lookup("test.run"), flag.Lookup("test.skip")
	return (run != nil && strings.Contains(run.Value.String(), "/")) || (skip != nil && skip.Value.String() != "")
}

// registryForced is the plain-keys fact [viaRegistry] records: the reader takes any fact that
// is not true as false, and countPath tells it apart from keys that are not plain.
type registryForced struct{}

// countPath runs fn, a fallback pass over inputs type typ on the path the reader chooses, and
// counts the path it took. The reader asks whether the keys are plain only once it has found no
// configuration file or custom source, so the pass was direct when that answer was yes.
func countPath(typ reflect.Type, fn func()) {
	switch got, ok := plainKeyFacts.Load(typ); {
	case got == registryForced{}:
		fn() // a registry pass the test asked for, not one the reader chose
		return
	case ok && got != true:
		fn()
		fallbackPathCounts.registry.Add(1)
		return
	}
	plainKeyFacts.Delete(typ)
	fn()
	if got, ok := plainKeyFacts.Load(typ); ok && got == true {
		fallbackPathCounts.direct.Add(1)
	} else {
		fallbackPathCounts.registry.Add(1)
	}
}

// viaRegistry runs fn with every fallback read of inputs type typ going through a registry when
// on is true, and as the reader chooses otherwise. It records [registryForced] as the type's
// plain-keys fact.
func viaRegistry(typ reflect.Type, on bool, fn func()) {
	if !on {
		fn()
		return
	}
	prev, had := plainKeyFacts.Load(typ)
	plainKeyFacts.Store(typ, registryForced{})
	defer func() {
		if had {
			plainKeyFacts.Store(typ, prev)
		} else {
			plainKeyFacts.Delete(typ)
		}
	}()
	fn()
}

// fallbackDiff describes how two fallback passes over copies of the same inputs differ, or
// returns "" when they do not.
func fallbackDiff(v, other reflect.Value, store, otherStore *parsedInputs, err, otherErr error) string {
	switch {
	case errChain(err) != errChain(otherErr):
		return fmt.Sprintf("error\n%s\nvs\n%s", errChain(err), errChain(otherErr))
	case !reflect.DeepEqual(any(err), any(otherErr)):
		return fmt.Sprintf("error fields %#v vs %#v", err, otherErr)
	case !reflect.DeepEqual(v.Interface(), other.Interface()):
		return fmt.Sprintf("values %+v vs %+v", v.Interface(), other.Interface())
	case store == nil || otherStore == nil:
		if store != otherStore {
			return "store"
		}
	case !reflect.DeepEqual(store.scopes, otherStore.scopes):
		return fmt.Sprintf("parsed values or origins %+v vs %+v", store.scopes, otherStore.scopes)
	case !reflect.DeepEqual(store.argvSet, otherStore.argvSet):
		return fmt.Sprintf("argv-set flags %v vs %v", store.argvSet, otherStore.argvSet)
	}
	return ""
}

// errChain is err and everything it wraps, each with its type, one per line.
func errChain(err error) string {
	var b strings.Builder
	var walk func(e error, depth int)
	walk = func(e error, depth int) {
		fmt.Fprintf(&b, "%*s%T: %v\n", 2*depth, "", e, e)
		switch u := e.(type) {
		case interface{ Unwrap() error }:
			if next := u.Unwrap(); next != nil {
				walk(next, depth+1)
			}
		case interface{ Unwrap() []error }:
			for _, next := range u.Unwrap() {
				walk(next, depth+1)
			}
		}
	}
	if err == nil {
		return "<nil>"
	}
	walk(err, 0)
	return b.String()
}

// cloneInputs is a copy of the inputs value v that shares no map, slice or pointer with it.
// Unexported fields are copied as they are.
func cloneInputs(v reflect.Value) reflect.Value {
	out := reflect.New(v.Type()).Elem()
	out.Set(deepCopy(v))
	return out
}

func deepCopy(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(deepCopy(v.Elem()))
		return out
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		for it := v.MapRange(); it.Next(); {
			out.SetMapIndex(it.Key(), deepCopy(it.Value()))
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			out.Index(i).Set(deepCopy(v.Index(i)))
		}
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := range v.NumField() {
			if f := out.Field(i); f.CanSet() {
				f.Set(deepCopy(v.Field(i)))
			}
		}
		return out
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(deepCopy(v.Elem()))
		return out
	}
	return v
}

// cloneStore is a copy of s that a fallback pass can change without changing s.
func cloneStore(s *parsedInputs) *parsedInputs {
	if s == nil {
		return nil
	}
	c := *s
	c.scopes = slices.Clone(s.scopes)
	for i, sc := range s.scopes {
		sc.flags = cloneSliceMap(sc.flags)
		sc.args = slices.Clone(sc.args)
		sc.typed = maps.Clone(sc.typed)
		sc.used = cloneSliceMap(sc.used)
		sc.origin = maps.Clone(sc.origin)
		sc.presenceOnly = maps.Clone(sc.presenceOnly)
		sc.handBuiltArgs = maps.Clone(sc.handBuiltArgs)
		sc.argOrigin = maps.Clone(sc.argOrigin)
		sc.written = maps.Clone(sc.written)
		sc.depValues = maps.Clone(sc.depValues)
		c.scopes[i] = sc
	}
	c.argvSet = slices.Clone(s.argvSet)
	for i, m := range s.argvSet {
		c.argvSet[i] = maps.Clone(m)
	}
	if s.span == &s.spanBuf {
		c.span = &c.spanBuf
	} else if s.span != nil {
		c.span = new(*s.span)
	}
	if s.detached != nil {
		c.detached = new(*s.detached)
	}
	if s.lateFlag != nil {
		c.lateFlag = new(*s.lateFlag)
	}
	if s.spelled != nil {
		c.spelled = new(*s.spelled)
	}
	c.pool = make([]string, len(s.pool), cap(s.pool))
	copy(c.pool, s.pool)
	return &c
}

func cloneSliceMap(m map[string][]string) map[string][]string {
	if m == nil {
		return nil
	}
	out := make(map[string][]string, len(m))
	for k, v := range m {
		out[k] = slices.Clone(v)
	}
	return out
}

// TestCloneStore_coversEveryField keeps cloneStore in step with the parsed store: a new field
// that holds a map, slice or pointer must be copied there too.
func TestCloneStore_coversEveryField(t *testing.T) {
	if n := reflect.TypeFor[scopeInputs]().NumField(); n != 12 {
		t.Errorf("scopeInputs has %d fields; cloneStore copies 12", n)
	}
	if n := reflect.TypeFor[parsedInputs]().NumField(); n != 15 {
		t.Errorf("parsedInputs has %d fields; cloneStore copies 15", n)
	}
}

// acmeProbes read each acme inputs type through Read and through InputsWithReport.
var acmeProbes = []func(mk func() *Context, meta InputSettings) probeResult{
	probeInputs[acApplyInputs], probeInputs[acCreateInputs], probeInputs[acDeleteInputs],
	probeInputs[acDeployInputs], probeInputs[acGetInputs], probeInputs[acImportInputs],
	probeInputs[acIngestInputs], probeInputs[acLoginInputs], probeInputs[acRunInputs],
	probeInputs[acWrapInputs],
}

// probeResult is everything a read of one inputs type shows.
type probeResult struct {
	read      any
	readErr   string
	merged    any
	reportErr string
	explain   string                      // the report as --explain-inputs prints it
	history   map[FieldPath][]InputSource // each field's history, winner last
}

// probeInputs reads T from a Context mk makes, once with Read and once with InputsWithReport.
func probeInputs[T any](mk func() *Context, meta InputSettings) probeResult {
	var r probeResult
	var in T
	r.readErr = errChain(NewInputReader(meta).Read(mk(), &in))
	r.read = in
	merged, report, err := mk().WithInputSettings(meta).InputsWithReport[T]()
	r.merged, r.reportErr = merged, errChain(err)
	var buf bytes.Buffer
	if ferr := report.Format(&buf); ferr != nil {
		buf.WriteString("format: ")
		buf.WriteString(ferr.Error())
	}
	r.explain = buf.String()
	r.history = map[FieldPath][]InputSource{}
	for _, f := range report.Fields() {
		r.history[f] = report.History(f)
	}
	return r
}

// checkProbesBothPaths reads every acme inputs type for the invocation mk makes, by the direct
// environment lookup and through the registry, and fails on any difference.
func checkProbesBothPaths(t *testing.T, mk func() *Context, meta InputSettings) {
	t.Helper()
	for i, probe := range acmeProbes {
		direct := probe(mk, meta)
		var viaReg probeResult
		viaRegistry(acmeProbeTypes[i], true, func() { viaReg = probe(mk, meta) })
		if !reflect.DeepEqual(direct, viaReg) {
			t.Errorf("%s: direct lookup and registry differ:\n direct:   %+v\n registry: %+v", acmeProbeTypes[i], direct, viaReg)
		}
	}
}

var acmeProbeTypes = typesOf(acApplyInputs{}, acCreateInputs{}, acDeleteInputs{}, acDeployInputs{},
	acGetInputs{}, acImportInputs{}, acIngestInputs{}, acLoginInputs{}, acRunInputs{}, acWrapInputs{})

func TestFallbackDiff_seesEveryDifference(t *testing.T) {
	type in struct{ A []string }
	base := reflect.ValueOf(&in{A: []string{"x"}}).Elem()
	store := &parsedInputs{scopes: []scopeInputs{{origin: map[string]string{"a": "env"}}}}
	if d := fallbackDiff(base, cloneInputs(base), store, cloneStore(store), nil, nil); d != "" {
		t.Fatalf("copies differ: %s", d)
	}
	changed := cloneInputs(base)
	changed.Field(0).Index(0).SetString("y")
	if fallbackDiff(base, changed, store, cloneStore(store), nil, nil) == "" {
		t.Error("a changed value went unseen")
	}
	if base.Field(0).Index(0).String() != "x" {
		t.Error("changing the copy changed the original")
	}
	moved := cloneStore(store)
	moved.scopes[0].origin["a"] = "config"
	if fallbackDiff(base, cloneInputs(base), store, moved, nil, nil) == "" {
		t.Error("a changed origin went unseen")
	}
	if store.scopes[0].origin["a"] != "env" {
		t.Error("changing the copied store changed the original")
	}
	if fallbackDiff(base, cloneInputs(base), store, cloneStore(store), errors.New("a"), nil) == "" {
		t.Error("a changed error went unseen")
	}
}
