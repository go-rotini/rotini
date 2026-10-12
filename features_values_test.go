package rotini

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// wantMissing checks that err is the WiringError of an unwired feature.
func wantMissing(t *testing.T, err error, feature, ctor string) {
	t.Helper()
	we, ok := errors.AsType[*WiringError](err)
	if !ok {
		t.Fatalf("err = %v (%T), want a *WiringError for %s", err, err, feature)
	}
	if we.Feature != feature || CategoryOf(err) != CategoryInternal ||
		!strings.Contains(we.Msg, "does not wire "+feature) || !strings.Contains(we.Msg, "rotini."+ctor) {
		t.Errorf("err = %+v, want feature %q naming rotini.%s, category internal", we, feature, ctor)
	}
}

// TestCoerce_valueKinds pins that each value kind reads only when its feature is wired, a
// pointer kind being built rather than filled, and that its error label comes from the kind.
func TestCoerce_valueKinds(t *testing.T) {
	var fields struct {
		URL      *url.URL
		Mail     mail.Address
		TZ       *time.Location
		MAC      net.HardwareAddr
		IP       netip.Addr
		Prefix   netip.Prefix
		AddrPort netip.AddrPort
		Re       *regexp.Regexp
	}
	v := reflect.ValueOf(&fields).Elem()
	for _, tt := range []struct {
		field, good, bad, feature, ctor, label string
		wire                                   func() Feature
	}{
		{"URL", "https://example.com", "example.com", "values:url", "URLValues()", "URL", URLValues},
		{"Mail", "ada@example.com", "nope", "values:email", "EmailValues()", "email address", EmailValues},
		{"TZ", "Europe/Berlin", "Mars/Base", "values:timezone", "TimeZoneValues()", "time zone", TimeZoneValues},
		{"MAC", "01:23:45:67:89:ab", "01:23", "values:mac", "MACValues()", "MAC address", MACValues},
		{"IP", "::1", "999.1.1.1", "values:ip", "IPValues()", "IP address", IPValues},
		{"Prefix", "10.0.0.0/8", "10.0.0.0", "values:ip", "IPValues()", "CIDR prefix", IPValues},
		{"AddrPort", "[::1]:80", "::1", "values:ip", "IPValues()", "address and port", IPValues},
		{"Re", "a+b", "a(", "values:regexp", "RegexpValues()", "regular expression", RegexpValues},
	} {
		f := v.FieldByName(tt.field)
		wantMissing(t, coerce(f, []string{tt.good}, noFeatures), tt.feature, tt.ctor)
		if got := typeLabel(f.Type(), noFeatures); got != f.Type().String() {
			t.Errorf("%s: unwired label %q, want the Go name", tt.field, got)
		}
		for _, fs := range []*featureSet{allFeatures(), newFeatureSet([]Feature{tt.wire()})} {
			f.SetZero()
			if err := coerce(f, []string{tt.good}, fs); err != nil || f.IsZero() {
				t.Errorf("%s %q: err = %v, value %v", tt.field, tt.good, err, f)
			}
			err := coerce(f, []string{tt.bad}, fs)
			if err == nil || !strings.Contains(err.Error(), "is not a valid "+tt.label) {
				t.Errorf("%s %q: err = %v, want it to name a %s", tt.field, tt.bad, err, tt.label)
			}
			if got := typeLabel(f.Type(), fs); got != tt.label {
				t.Errorf("%s: label %q, want %q", tt.field, got, tt.label)
			}
		}
	}

	// A kind's pointer type is built (not filled through a nil pointer); a pointer to a value
	// kind reads through it; a named pointer type is not the kind.
	var p struct {
		URL  **url.URL
		Mail *mail.Address
		Own  ownLocation
	}
	pv := reflect.ValueOf(&p).Elem()
	if err := coerce(pv.Field(0), []string{"https://x.dev"}, allFeatures()); err != nil || (*p.URL).Host != "x.dev" {
		t.Errorf("**url.URL: err = %v", err)
	}
	if err := coerce(pv.Field(1), []string{"ada@x.dev"}, allFeatures()); err != nil || p.Mail.Address != "ada@x.dev" {
		t.Errorf("*mail.Address: err = %v", err)
	}
	if err := coerce(pv.Field(2), []string{"UTC"}, noFeatures); err == nil || errors.As(err, new(*WiringError)) {
		t.Errorf("named pointer type: err = %v, want the type's own error, not a WiringError", err)
	}
	if got := typeLabel(pv.Field(2).Type(), allFeatures()); got != "rotini.ownLocation" {
		t.Errorf("named pointer type label %q", got)
	}

	// time.Time and rotini's own types stay core.
	var core struct {
		At   time.Time
		Size ByteSize
	}
	cv := reflect.ValueOf(&core).Elem()
	if err := coerce(cv.Field(0), []string{"2026-01-02T03:04:05Z"}, noFeatures); err != nil {
		t.Errorf("time.Time unwired: %v", err)
	}
	if err := coerce(cv.Field(0), []string{"nope"}, noFeatures); err == nil || !strings.Contains(err.Error(), "RFC 3339") {
		t.Errorf("time.Time bad value: %v", err)
	}
	if got := typeLabel(cv.Field(1).Type(), noFeatures); got != "size" {
		t.Errorf("ByteSize label %q", got)
	}
}

type ownLocation *time.Location

// TestParse_unwiredValueKind pins the WiringError a wired program gets for an input of a value
// kind it does not wire, naming the input.
func TestParse_unwiredValueKind(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "home", Identifiers: []string{"--home"}, Type: "*url.URL"}}}
	var in struct {
		App struct {
			Flags struct {
				Home *url.URL `rotini:"home"`
			}
			Arguments struct{}
		}
	}
	err := NewParser().Parse(wired(NewContextFor(def, []string{"--home", "https://x.dev"})), &in)
	wantMissing(t, err, "values:url", "URLValues()")
	if !strings.HasPrefix(err.Error(), "input --home is a URL, but the program does not wire values:url") {
		t.Errorf("message %q", err)
	}
	if err := NewParser().Parse(wired(NewContextFor(def, []string{"--home", "https://x.dev"}), URLValues()), &in); err != nil || in.App.Flags.Home.Host != "x.dev" {
		t.Errorf("wired URLValues: err = %v", err)
	}
}

// TestCoerceFailure_passesWiringError pins that a WiringError, wrapped or not, comes out of
// coerceFailure and configValueError as itself.
func TestCoerceFailure_passesWiringError(t *testing.T) {
	we := missingKind("ip", "IP address")
	for _, err := range []error{we, fmt.Errorf("value for key %q: %w", "k", we)} {
		got := coerceFailure("--addr", "--addr", err, false)
		gw, ok := got.(*WiringError)
		if !ok || gw.Feature != "values:ip" || !strings.HasPrefix(gw.Msg, "input --addr is an IP address, but") {
			t.Errorf("coerceFailure(%v) = %v (%T)", err, got, got)
		}
	}
	other := missingFeature("bounds", "Bounds()", "input --n declares bounds")
	if got := coerceFailure("--n", "--n", other, false); got != other {
		t.Errorf("coerceFailure kept %v, want the error itself", got)
	}
	if got := configValueError("k", fmt.Errorf("x: %w", other)); got != other {
		t.Errorf("configValueError = %v, want the WiringError itself", got)
	}
}

// TestCheckConstraints_wiring pins the order of the per-value checks and that each needs its
// feature only when it would check something.
func TestCheckConstraints_wiring(t *testing.T) {
	c := Constraints{MaxLength: new(3), Pattern: "^a", MaxItems: new(5)}
	vals := []string{"abc", "bcd", "abcdef"}
	full := newFeatureSet([]Feature{Bounds(), Patterns()})
	want := checkConstraints(allFeatures(), "--s", "[]string", "", c, vals, false, "")
	if want == nil || !strings.Contains(want.Error(), "must match ^a") {
		t.Fatalf("legacy = %v, want the pattern error on the second value", want)
	}
	if got := checkConstraints(full, "--s", "[]string", "", c, vals, false, ""); errText(got) != errText(want) {
		t.Errorf("wired = %v, want %v", got, want)
	}
	vals = []string{"abcdef", "bcd"}
	if got := checkConstraints(full, "--s", "[]string", "", c, vals, false, ""); got == nil || !strings.Contains(got.Error(), "at most 3") {
		t.Errorf("length before pattern: got %v", got)
	}

	wantMissing(t, checkConstraints(noFeatures, "--s", "[]string", "", Constraints{MinItems: 1}, nil, false, ""), "bounds", "Bounds()")
	wantMissing(t, checkConstraints(noFeatures, "--n", "int", "", Constraints{Minimum: new(1.0)}, []string{"5"}, false, ""), "bounds", "Bounds()")
	wantMissing(t, checkConstraints(noFeatures, "--d", "time.Duration", "", Constraints{Maximum: new(1.0)}, []string{"1s"}, false, ""), "bounds", "Bounds()")
	wantMissing(t, checkConstraints(noFeatures, "--s", "string", "", Constraints{Pattern: "x"}, []string{"x"}, false, ""), "patterns", "Patterns()")
	wantMissing(t, checkConstraints(noFeatures, "<in>", "inputfile", "", Constraints{}, []string{"-"}, false, ""), "stream-paths", "StreamPaths()")

	// A constraint that checks nothing for its type, or no value to check, needs no feature.
	for _, tt := range []struct {
		typ  string
		c    Constraints
		vals []string
	}{
		{"bool", Constraints{Minimum: new(1.0), Pattern: "x"}, []string{"true"}},
		{"int", Constraints{MinLength: 2, Pattern: "x"}, []string{"5"}},
		{"string", Constraints{Minimum: new(1.0)}, []string{"x"}},
		{"string", Constraints{MaxLength: new(1), Pattern: "x"}, nil},
		{"string", Constraints{MinItems: 1}, []string{"x"}},
		{"[]string", Constraints{UniqueItems: true}, []string{"x", "x"}},
	} {
		if err := checkConstraints(noFeatures, "--x", tt.typ, "", tt.c, tt.vals, false, ""); err != nil {
			t.Errorf("%s %+v: %v, want nothing to check", tt.typ, tt.c, err)
		}
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkConstraints(noFeatures, "--f", "existingfile", "", Constraints{}, []string{"f", "missing"}, false, dir); err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Errorf("existingfile stays core: %v", err)
	}
}

// TestValidate_wiring pins the WiringErrors of a command line that needs unwired validation:
// unique items, flag groups, flag dependencies, the one-stdin rule and object flags.
func TestValidate_wiring(t *testing.T) {
	type flags struct {
		Tags []string `rotini:"tags"`
		A    bool     `rotini:"a"`
		B    bool     `rotini:"b"`
		In   string   `rotini:"in"`
	}
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "tags", Identifiers: []string{"--tags"}, Type: "[]string", UniqueItems: true},
			{Name: "a", Identifiers: []string{"--a"}, Type: "bool"},
			{Name: "b", Identifiers: []string{"--b"}, Type: "bool"},
			{Name: "in", Identifiers: []string{"--in"}, Type: "inputfile"},
		},
		FlagGroups:       []FlagGroup{{Kind: FlagGroupMutuallyExclusive, Flags: []string{"a", "b"}}},
		FlagDependencies: []FlagDependency{{When: "a", Requires: []string{"b"}}},
	}
	parse := func(fs []Feature, argv ...string) error {
		var in struct {
			App struct {
				Flags     flags
				Arguments struct{}
			}
		}
		return NewParser().Parse(wired(NewContextFor(def, argv), fs...), &in)
	}
	all := []Feature{Bounds(), FlagGroups(), FlagDependencies(), StreamPaths()}
	without := func(name string) []Feature {
		var out []Feature
		for _, f := range all {
			if f.String() != name {
				out = append(out, f)
			}
		}
		return out
	}
	wantMissing(t, parse(without("bounds"), "--tags", "x", "--tags", "y"), "bounds", "Bounds()")
	wantMissing(t, parse(without("flag-groups")), "flag-groups", "FlagGroups()")
	wantMissing(t, parse(without("flag-dependencies")), "flag-dependencies", "FlagDependencies()")
	wantMissing(t, parse(without("stream-paths")), "stream-paths", "StreamPaths()")
	if err := parse(all, "--tags", "x", "--tags", "x"); err == nil || !strings.Contains(err.Error(), "must not repeat") {
		t.Errorf("wired unique: %v", err)
	}
	if err := parse(all, "--a", "--b"); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("wired groups: %v", err)
	}
	if err := parse(all, "--tags", "x"); err != nil {
		t.Errorf("wired: %v", err)
	}

	var obj objInputs
	err := NewParser().Parse(wired(NewContextFor(objDef(), []string{"--db", `{"host":"h"}`})), &obj)
	wantMissing(t, err, "object-flags", "ObjectFlags()")
	err = NewParser().Parse(wiredFor(NewContextFor(objDef(), []string{"--db", `{"host":"h"}`}), objDef(), InputSettings{}, reflect.TypeFor[objInputs]()), &obj)
	if err != nil || obj.App.Flags.DB.Host != "h" {
		t.Errorf("wired object flag: %v", err)
	}
}

// TestUniqueKeys_followKinds pins that the netip and MAC kinds canonicalize unique values only
// where they are wired, as the values themselves read only there.
func TestUniqueKeys_followKinds(t *testing.T) {
	ts := timeSpec{clock: featureClock(newFeatureSet([]Feature{IPValues(), MACValues()}))}
	for _, tt := range [][3]string{
		{"[]netip.Addr", "::1", "0:0:0:0:0:0:0:1"},
		{"[]netip.Prefix", "10.0.0.0/8", "10.0.0.0/8"},
		{"[]netip.AddrPort", "[::1]:80", "[0::1]:80"},
		{"[]net.HardwareAddr", "01:23:45:67:89:AB", "01-23-45-67-89-ab"},
	} {
		key := uniqueKeyFor(tt[0], ts, enumSet{})
		if key(tt[1]) != key(tt[2]) {
			t.Errorf("%s: %q and %q are different keys", tt[0], tt[1], tt[2])
		}
		if bare := uniqueKeyFor(tt[0], timeSpec{}, enumSet{}); bare(tt[1]) != tt[1] {
			t.Errorf("%s unwired: key %q, want the value as written", tt[0], bare(tt[1]))
		}
	}
}

// TestFeatureClock pins that a feature clock carries its features and reads time.Now on every
// use, as no clock does.
func TestFeatureClock(t *testing.T) {
	c := featureClock(allFeatures())
	if c.features() != allFeatures() {
		t.Error("featureClock lost its features")
	}
	first := c.now()
	time.Sleep(2 * time.Millisecond)
	if !c.now().After(first) {
		t.Error("featureClock cached its reading")
	}
}

// TestMeasuredFor pins the measured types.
func TestMeasuredFor(t *testing.T) {
	if m := measuredFor("time.Duration"); m == nil || m.format(float64(36*time.Hour)) != "1d12h" {
		t.Error("time.Duration is not measured as a duration")
	}
	if n, ok := measuredFor("rotini.ByteSize").parse("1Ki"); !ok || n != 1024 {
		t.Errorf("ByteSize reads 1Ki as %v, %v", n, ok)
	}
	for _, typ := range []string{"int", "string", "ByteSize", ""} {
		if measuredFor(typ) != nil {
			t.Errorf("%q is measured", typ)
		}
	}
}
