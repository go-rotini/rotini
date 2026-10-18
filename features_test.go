package rotini

import (
	"errors"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-rotini/recon"
)

func featureNames(fs []Feature) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.String()
	}
	return out
}

func TestFeature_String(t *testing.T) {
	got := featureNames([]Feature{
		Plugins(), ResponseFiles(), Chdir(), Multicall(), Stdin("json", "yaml"), Env(), ConfigFiles(), Profiles(),
		EnvFiles(), Fallbacks(), PathExpansion(), ObjectFlags(), Schemas(), JSON(), YAML(), TOML(), JSONC(), Dotenv(),
		Bounds(), UniqueItems(), Patterns(), FlagGroups(), FlagDependencies(), StreamPaths(), URLValues(), EmailValues(),
		TimeZoneValues(), MACValues(), IPValues(), RegexpValues(),
	})
	want := []string{
		"plugins", "response-files", "chdir", "multicall", "stdin(json,yaml)", "env", "config-files", "config-profiles",
		"env-files", "fallbacks", "path-expansion", "object-flags", "schemas", "codec:json", "codec:yaml", "codec:toml",
		"codec:jsonc", "codec:dotenv", "bounds", "unique-items", "patterns", "flag-groups", "flag-dependencies", "stream-paths",
		"values:url", "values:email", "values:timezone", "values:mac", "values:ip", "values:regexp",
	}
	if !slices.Equal(got, want) {
		t.Errorf("names = %q\nwant    %q", got, want)
	}
	if got := Stdin().String(); got != "stdin(text,lines,bytes,json,jsonl,yaml,jsonc,toml)" {
		t.Errorf("Stdin() = %q, want every format", got)
	}
}

// hookSlots reports, for each hook struct of s, whether it is set.
func hookSlots(s *featureSet) []bool {
	return []bool{
		s.plugins != nil, s.respFiles != nil, s.chdir != nil, s.multicall != nil, s.stdin != nil, s.env != nil,
		s.config != nil, s.profiles != nil, s.envFiles != nil, s.fallbacks != nil, s.channels != nil, s.expand != nil,
		s.objects != nil, s.schemas != nil, s.bounds != nil, s.unique != nil, s.patterns != nil, s.groups != nil, s.deps != nil,
		s.streamPaths != nil,
	}
}

func TestAllFeatures(t *testing.T) {
	s := allFeatures()
	if !s.all || s != allFeatures() {
		t.Fatal("allFeatures is not one shared set marked all")
	}
	for i, set := range hookSlots(s) {
		if !set {
			t.Errorf("hook %d is not wired", i)
		}
	}
	var codecs []string
	for _, c := range s.codecs {
		codecs = append(codecs, c.name)
	}
	if want := []string{"yaml", "toml", "jsonc", "json", "dotenv"}; !slices.Equal(codecs, want) {
		t.Errorf("codecs = %q, want recon's default order %q", codecs, want)
	}
	if got := s.reconCodecs().Names(); !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(codecs))) {
		t.Errorf("reconCodecs = %q, want %q", got, codecs)
	}
	for _, ext := range []string{".json", ".yaml", ".yml", ".toml", ".jsonc", ".json5", ".env"} {
		got, _ := s.reconCodecs().ByExtension(ext)
		want, _ := recon.DefaultCodecs().ByExtension(ext)
		if got == nil || want == nil || got.Name() != want.Name() {
			t.Errorf("extension %s reads as %v, want %v", ext, got, want)
		}
	}
	if !slices.Equal(s.stdinFormats, stdinFormats) {
		t.Errorf("stdin formats = %q", s.stdinFormats)
	}
	if len(allKinds()) != 8 {
		t.Errorf("allKinds has %d kinds, want 8", len(allKinds()))
	}
}

func TestNewFeatureSet_onlyWhatIsListed(t *testing.T) {
	if slices.Contains(hookSlots(newFeatureSet(nil)), true) {
		t.Error("an empty set wires a hook")
	}
	s := newFeatureSet([]Feature{Env(), YAML(), YAML(), Stdin("yaml"), Stdin("json", "yaml"), IPValues(), IPValues()})
	if s.env == nil || s.channels == nil || s.config != nil {
		t.Error("Env wires the env and channel hooks only")
	}
	if len(s.codecs) != 1 || s.codec("yaml") == nil || s.codec("json") != nil {
		t.Errorf("codecs = %v, want yaml once", s.codecs)
	}
	if !s.hasStdinFormat("json") || !s.hasStdinFormat("yaml") || s.hasStdinFormat("toml") || !slices.Equal(s.stdinFormats, []string{"yaml", "json"}) {
		t.Errorf("stdin formats = %q", s.stdinFormats)
	}
	if len(s.kinds) != 3 {
		t.Errorf("IPValues twice wires %d kinds, want 3", len(s.kinds))
	}
	if noFeatures.hasStdinFormat("json") || noFeatures.codec("json") != nil {
		t.Error("noFeatures wires something")
	}
}

func TestFeatureSet_kinds(t *testing.T) {
	s := allFeatures()
	cases := []struct {
		typ   reflect.Type
		id    string
		label string
	}{
		{reflect.TypeFor[*url.URL](), "url", "URL"},
		{reflect.TypeFor[mail.Address](), "email", "email address"},
		{reflect.TypeFor[*time.Location](), "timezone", "time zone"},
		{reflect.TypeFor[net.HardwareAddr](), "mac", "MAC address"},
		{reflect.TypeFor[netip.Addr](), "ip", "IP address"},
		{reflect.TypeFor[netip.Prefix](), "ip", "CIDR prefix"},
		{reflect.TypeFor[netip.AddrPort](), "ip", "address and port"},
		{reflect.TypeFor[*regexp.Regexp](), "regexp", "regular expression"},
	}
	for _, c := range cases {
		id, label, ok := extraKindOf(c.typ)
		if !ok || id != c.id || label != c.label {
			t.Errorf("extraKindOf(%v) = %q, %q, %v; want %q, %q", c.typ, id, label, ok, c.id, c.label)
		}
		k := s.kindFor(c.typ)
		if k == nil || k.id != c.id || k.label != c.label {
			t.Errorf("kindFor(%v) = %+v", c.typ, k)
		}
		if noFeatures.kindFor(c.typ) != nil {
			t.Errorf("noFeatures has a kind for %v", c.typ)
		}
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[url.URL](), reflect.TypeFor[*mail.Address](), reflect.TypeFor[string](), reflect.TypeFor[time.Time]()} {
		if _, _, ok := extraKindOf(typ); ok || s.kindFor(typ) != nil {
			t.Errorf("%v is a value kind", typ)
		}
	}
	if v, err := s.kindFor(reflect.TypeFor[*url.URL]()).parse("https://example.com"); err != nil || v.Interface().(*url.URL).Host != "example.com" {
		t.Errorf("url parse = %v, %v", v, err)
	}
	if k, err := s.kindFor(reflect.TypeFor[netip.Prefix]()).unique("10.0.0.0/8"); err != nil || k != "10.0.0.0/8" {
		t.Errorf("prefix unique = %q, %v", k, err)
	}
}

func TestMissingFeature(t *testing.T) {
	err := missingFeature("codec:yaml", "YAML()", `configuration file "app" is yaml`)
	want := "configuration file \"app\" is yaml, but the program does not wire codec:yaml; regenerate it with `rotini generate`, " +
		"or add rotini.YAML() to the features passed to rotini.NewWiredProgram"
	if err.Error() != want || err.Feature != "codec:yaml" {
		t.Errorf("missingFeature = %q (feature %q)", err.Error(), err.Feature)
	}
	if CategoryOf(err) != CategoryInternal || !errors.Is(err, ErrInternal) {
		t.Error("a missing feature is not an internal error")
	}
}

func TestProgramFeatures(t *testing.T) {
	def := Definition{Name: "app", Handler: "App"}
	if p := NewProgramFunc(def, nil); p.feats != allFeatures() {
		t.Error("NewProgramFunc does not have every feature")
	}
	if p := NewProgram(def, nil); p.feats != allFeatures() {
		t.Error("NewProgram does not have every feature")
	}
	if rtx := NewContextFor(def, nil); rtx.features() != allFeatures() {
		t.Error("NewContextFor does not have every feature")
	}
	var nilCtx *Context
	if nilCtx.features() != noFeatures || (&runClock{}).features() != noFeatures {
		t.Error("a Context without a program has features")
	}

	legacy := NewProgramFunc(def, nil).WithFeatures(Env())
	if legacy.feats != allFeatures() || len(allFeatures().kinds) != 8 {
		t.Error("WithFeatures changed the every-feature set")
	}

	p := NewWiredProgram(def, nil, Env())
	if p.feats.env == nil || p.feats.config != nil {
		t.Fatal("NewWiredProgram does not have exactly its features")
	}
	p.WithFeatures(ConfigFiles(), YAML())
	if p.feats.config == nil || p.feats.codec("yaml") == nil {
		t.Error("WithFeatures did not add to a wired program")
	}
	if rtx := p.newRunContext(); rtx.features() != p.feats || rtx.osView().clockRef().features() != p.feats {
		t.Error("a run does not carry its program's features")
	}
	if !strings.Contains(p.noHandlers, "NewWiredProgram") {
		t.Errorf("noHandlers = %q, want it to name NewWiredProgram", p.noHandlers)
	}
}

func TestWiredContext(t *testing.T) {
	def := Definition{Name: "app", Handler: "App"}
	rtx := wiredFor(NewContextFor(def, nil), def, InputSettings{}, typesOf(needsKitchenInputs{})...)
	s := rtx.features()
	if s.all || s.env == nil || s.config == nil || s.plugins != nil || !s.hasStdinFormat("jsonl") {
		t.Errorf("wiredFor did not give exactly the needed features")
	}
	if wired(rtx).features().env != nil {
		t.Error("wired with nothing kept a feature")
	}
}

func TestValueKinds_spellGenerically(t *testing.T) {
	for _, k := range allKinds() {
		if k.text != nil {
			t.Errorf("kind %s has its own spelling; ArgvOf spells every kind the generic way", k.label)
		}
	}
}
