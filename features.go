package rotini

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/go-rotini/recon"
)

// Feature is one optional part of the runtime. A program built with [NewWiredProgram] links
// and runs only the features it is given; one built with [NewProgram] or [NewProgramFunc], and
// a [Context] from [NewContextFor], has every feature. The generated NewProgram passes the
// features its spec uses.
//
// The constructors group into:
//
//   - program — [Plugins], [ResponseFiles], [Chdir], [Multicall]
//   - input channels — [Stdin], [Env], [ConfigFiles], [Profiles], [EnvFiles],
//     [Fallbacks], [PathExpansion], [ObjectFlags], [Schemas]
//   - document formats — [JSON], [YAML], [TOML], [JSONC], [Dotenv]
//   - validation — [Bounds], [UniqueItems], [Patterns], [FlagGroups], [FlagDependencies],
//     [StreamPaths]
//   - value types — [URLValues], [EmailValues], [TimeZoneValues], [MACValues], [IPValues],
//     [RegexpValues]
//
// A run that needs a feature its program was not given fails with a [*WiringError] whose
// Feature names it.
type Feature struct {
	name  string
	apply func(*featureSet)
}

// String names the feature: "plugins", "stdin(json,yaml)", "codec:yaml", "values:url".
func (f Feature) String() string { return f.name }

// featureSet is the features one program wires: each hook struct is nil when its feature is
// not wired. The legacy constructors share one set with every feature ([allFeatures]).
type featureSet struct {
	all bool // allFeatures(): WithFeatures is a no-op; messages never needed

	plugins   *pluginHooks
	respFiles *responseFileHooks
	chdir     *chdirHooks
	multicall *multicallHooks

	stdin        *stdinHooks
	stdinFormats []string
	codecs       []*codecEntry // wired codecs, in wiring order
	codecSet     struct {
		once sync.Once
		set  *recon.Codecs
	} // built on first use
	env       *envHooks
	config    *configHooks
	profiles  *profileHooks
	envFiles  *envFileHooks
	fallbacks *fallbackHooks
	channels  *channelHooks // set by Env() and by ConfigFiles() (same value)
	expand    *expandHooks
	objects   *objectHooks
	schemas   *schemaHooks

	bounds      *boundHooks
	unique      *uniqueHooks
	patterns    *patternHooks
	groups      *groupHooks
	deps        *dependencyHooks
	streamPaths *streamPathHooks

	kinds []*valueKind
}

// The hook structs hold each feature's implementation as func fields. Each constructor
// allocates its struct inside its own body: a package-level value holding these funcs would
// keep the implementation linked in every binary.

type pluginHooks struct {
	exec     func(p *Program, ctx context.Context, rtx *Context, chain []Command, d *PluginDispatch) (int, error)
	lookPath func(name string) (string, bool) // process PATH: exec.LookPath
	discover func(d *PluginDiscoveryDef, pluginPath string, view *osView) ([]DiscoveredPlugin, []error)
}

type responseFileHooks struct {
	expand          func(argv []string, prefix string, view *osView, skipUnreadable bool) ([]string, error)
	completionWords func(words []string, prefix string, view *osView) ([]string, bool)
}

type chdirHooks struct {
	apply    func(p *Program, rtx *Context, chain []Command, argv []string) ([]string, error)
	complete func(p *Program, rtx *Context, typed []string)
}

type multicallHooks struct {
	route func(def Definition, argv0 string, argv []string, goos string) ([]string, bool)
}

type stdinHooks struct {
	// fill is fillStdin after its "leaf has a Stdin field with a stdin tag, not waived" checks.
	fill func(b *InputReader, rtx *Context, sf reflect.Value, tag stdinTag, argValues func(string) ([]string, error)) error
}

type codecEntry struct {
	name  string             // "json", "yaml", "toml", "jsonc", "dotenv"
	codec func() recon.Codec // the format's recon codec
	// decodeAny decodes a document of any shape, for a stdin payload that is not an object;
	// nil for toml and dotenv, whose documents are always tables.
	decodeAny func(data []byte) (any, error)
}

type envHooks struct {
	registry func(v reflect.Value, envPrefix string, view *osView) (*recon.Registry, error)
}

type configHooks struct {
	regs        func(b *InputReader, chain []Command, boot fileBootstrap, v reflect.Value, waived bool, view *osView) (*cfgRegs, error)
	fileSources func(b *InputReader, files []ConfigFile, boot fileBootstrap, waived bool, view *osView) ([]recon.Source, error)
	locate      func(rtx *Context, name string) (locatedConfigFile, error)
}

type profileHooks struct {
	choices func(b *InputReader, chain []Command, store *parsedInputs, view *osView) map[string]profileChoice
	check   func(b *InputReader, v reflect.Value, chain []Command, boot fileBootstrap, waived bool, view *osView) error
	names   func(f ConfigFile, src recon.Source, path string) ([]string, error)
	sources func(f ConfigFile, src recon.Source, path string, names []string, choice profileChoice) []recon.Source
}

type envFileHooks struct {
	view func(b *InputReader, chain []Command, v reflect.Value, overrides map[string]pathOverride, waived bool, view *osView) (*osView, error)
}

type fallbackHooks struct {
	reconcile func(b *InputReader, v reflect.Value, chain []Command, store *parsedInputs, boot fileBootstrap, anchor int, waived bool, view *osView) error
	flagEnv   func(v reflect.Value, envPrefix string, view *osView) recon.Source
}

type channelHooks struct {
	fill     func(v reflect.Value, envReg *recon.Registry, cfg *cfgRegs, waived bool, labels channelLabels) error
	validate func(v reflect.Value, envReg *recon.Registry, cfg *cfgRegs, view *osView) error
}

type expandHooks struct {
	store      func(v reflect.Value, chain []Command, store *parsedInputs, anchor int, view *osView) error
	channels   func(v reflect.Value, envReg *recon.Registry, cfg *cfgRegs, waived bool, labels channelLabels) error
	fallback   func(vals []string, tag reflect.StructTag, source, label string, rd fallbackRead) ([]string, error)
	lookupUser func(name string) (homeDir string, err error)
}

type objectHooks struct {
	bind func(f reflect.Value, raw []string, fd FlagDef, fs *featureSet) error
}

// mapValidator checks a decoded document.
type mapValidator interface{ Validate(m map[string]any) error }

type schemaHooks struct {
	validator       func(schema string) (mapValidator, error)
	document        func(schema string, raw []byte) error
	record          func(schema string, v any) error
	patternMessages func(schema string, err error)
}

type boundHooks struct {
	items func(label string, c Constraints, n int) error
	value func(label, elem, base string, c Constraints, v string, secret bool) error // numeric, measured, length
}

type uniqueHooks struct {
	values    func(label string, vals []string, key func(string) string, secret bool) error
	key       func(typ string, ts timeSpec, enum enumSet) func(string) string
	typed     func(label string, elems []reflect.Value, secret bool) error
	documents func(docs []map[string]any) string
}

type patternHooks struct {
	match func(pattern, v string) (bool, error)
}

type groupHooks struct {
	validate func(chain []Command, store *parsedInputs) error
}

type dependencyHooks struct {
	validate func(chain []Command, store *parsedInputs) error
}

type streamPathHooks struct {
	check    func(label, typ, value, dir string) error
	dashOnce func(chain []Command, store *parsedInputs) error
}

// kindType names a Go type by package path and name, never by reflect.TypeFor, so a kind that
// is not wired keeps no methods of its type linked.
type kindType struct {
	pkg, name string
	ptr       bool
}

// valueKind is one extra value type: how it parses, how its values compare for uniqueness,
// and how ArgvOf spells one.
type valueKind struct {
	id, label string // "url", "URL"
	types     []kindType
	parse     func(string) (reflect.Value, error) // nil: the type parses itself
	unique    func(string) (string, error)        // canonical key; nil: as written
	text      func(v reflect.Value) (string, error)
}

// noFeatures is the set of a Context with no program.
var noFeatures = &featureSet{}

// newFeatureSet is the set of exactly fs.
func newFeatureSet(fs []Feature) *featureSet {
	s := &featureSet{}
	for _, f := range fs {
		if f.apply != nil {
			f.apply(s)
		}
	}
	return s
}

var (
	allOnce sync.Once
	allSet  *featureSet
)

// allFeatures is the set with every feature, every stdin format and every codec, in the order
// that gives recon's default extension precedence. Built once, on first use.
func allFeatures() *featureSet {
	allOnce.Do(func() {
		s := newFeatureSet([]Feature{
			Plugins(), ResponseFiles(), Chdir(), Multicall(),
			Stdin(), Env(), ConfigFiles(), Profiles(), EnvFiles(), Fallbacks(),
			PathExpansion(), ObjectFlags(), Schemas(),
			YAML(), TOML(), JSONC(), JSON(), Dotenv(),
			Bounds(), UniqueItems(), Patterns(), FlagGroups(), FlagDependencies(), StreamPaths(),
			URLValues(), EmailValues(), TimeZoneValues(), MACValues(), IPValues(), RegexpValues(),
		})
		s.all = true
		allSet = s
	})
	return allSet
}

// legacyFeatures is the set [NewProgram], [NewProgramFunc] and [NewContextFor] give a program
// or Context for def: every feature. Tests replace it to run a suite with only the features
// def needs.
var legacyFeatures = func(Definition) *featureSet { return allFeatures() }

// add wires more features into s; a no-op for the set with every feature.
func (s *featureSet) add(fs []Feature) {
	if s.all {
		return
	}
	for _, f := range fs {
		if f.apply != nil {
			f.apply(s)
		}
	}
}

// codec is the wired codec named name, nil when it is not wired.
func (s *featureSet) codec(name string) *codecEntry {
	for _, c := range s.codecs {
		if c.name == name {
			return c
		}
	}
	return nil
}

// addCodec wires c unless a codec of its name already is.
func (s *featureSet) addCodec(c *codecEntry) {
	if s.codec(c.name) == nil {
		s.codecs = append(s.codecs, c)
	}
}

// reconCodecs is the wired codecs as a recon codec set, in wiring order, fixed at its first use.
func (s *featureSet) reconCodecs() *recon.Codecs {
	s.codecSet.once.Do(func() {
		if len(s.codecs) == 0 {
			s.codecSet.set = noCodecs() // rotini never adds a codec to a set it passes recon
			return
		}
		cs := make([]recon.Codec, len(s.codecs))
		for i, c := range s.codecs {
			cs[i] = c.codec()
		}
		s.codecSet.set = recon.NewCodecs(cs...)
	})
	return s.codecSet.set
}

// noCodecs is the codec set of a program that wires none, shared by them all.
func noCodecs() *recon.Codecs {
	noCodecsOnce.Do(func() { noCodecsSet = recon.NewCodecs() })
	return noCodecsSet
}

var (
	noCodecsOnce sync.Once
	noCodecsSet  *recon.Codecs
)

// hasStdinFormat reports whether stdin may be read as format.
func (s *featureSet) hasStdinFormat(name string) bool {
	return s.stdin != nil && slices.Contains(s.stdinFormats, name)
}

// addKind wires k unless a kind for one of its types already is.
func (s *featureSet) addKind(k *valueKind) {
	for _, have := range s.kinds {
		if have.id == k.id && slices.Equal(have.types, k.types) {
			return
		}
	}
	s.kinds = append(s.kinds, k)
}

// kindFor is the wired kind of field type t (a pointer kind matches only the pointer type),
// nil when none is wired.
func (s *featureSet) kindFor(t reflect.Type) *valueKind {
	want := kindTypeOf(t)
	for _, k := range s.kinds {
		if slices.Contains(k.types, want) {
			return k
		}
	}
	return nil
}

// kindTypeOf names t, or the type t points to, as a kindType.
func kindTypeOf(t reflect.Type) kindType {
	if t.Kind() == reflect.Pointer {
		e := t.Elem()
		return kindType{pkg: e.PkgPath(), name: e.Name(), ptr: true}
	}
	return kindType{pkg: t.PkgPath(), name: t.Name()}
}

// extraKindOf reports the value kind field type t belongs to, wired or not: the kind's
// feature id and the label its errors use. It compares type names only.
func extraKindOf(t reflect.Type) (id, label string, ok bool) {
	kt := kindTypeOf(t)
	switch kt.pkg + "." + kt.name {
	case "net/url.URL":
		return "url", "URL", kt.ptr
	case "net/mail.Address":
		return "email", "email address", !kt.ptr
	case "time.Location":
		return "timezone", "time zone", kt.ptr
	case "net.HardwareAddr":
		return "mac", "MAC address", !kt.ptr
	case "net/netip.Addr":
		return "ip", "IP address", !kt.ptr
	case "net/netip.Prefix":
		return "ip", "CIDR prefix", !kt.ptr
	case "net/netip.AddrPort":
		return "ip", "address and port", !kt.ptr
	case "regexp.Regexp":
		return "regexp", "regular expression", kt.ptr
	}
	return "", "", false
}

// missingFeature is the error for a run that needs feature, which its program does not wire:
// subject says what needs it, ctor is the constructor that wires it.
func missingFeature(feature, ctor, subject string) *WiringError {
	return &WiringError{
		Feature: feature,
		Msg: fmt.Sprintf("%s, but the program does not wire %s; regenerate it with `rotini generate`, "+
			"or add rotini.%s to the features passed to rotini.NewWiredProgram", subject, feature, ctor),
	}
}

// features is the run's feature set; none for a nil Context.
func (rtx *Context) features() *featureSet {
	if rtx == nil {
		return noFeatures
	}
	return rtx.clock.features()
}

// stdinFeatureName names a Stdin feature by its formats.
func stdinFeatureName(formats []string) string {
	return "stdin(" + strings.Join(formats, ",") + ")"
}
