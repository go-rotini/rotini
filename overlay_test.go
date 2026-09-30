package rotini

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// Overlay-fixture shapes: one command "app" exercising every channel a layer
// can supply — a reconciled flag (argv/env/files/default), a plain argv-only
// flag, a secret flag, a defaulted positional, an env input, and a config
// input with a recon default.
type ovFlags struct {
	Color string `rotini:"color" recon:"app.color"`
	Out   string `rotini:"out"`
	Token string `rotini:"token" recon:"app.token,secret"`
}
type ovArgs struct {
	Name string `rotini:"name"`
}
type ovEnv struct {
	Region string `rotini:"region" recon:"region"`
}
type ovConfig struct {
	Retries int `rotini:"retries" recon:"app.retries,default=3"`
}
type ovCmd struct {
	Flags     ovFlags
	Arguments ovArgs
	Env       ovEnv
	Config    ovConfig
}
type ovInputs struct{ App ovCmd }

func ovDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "color", Identifiers: []string{"--color"}, Type: "string", Default: "blue", Enum: []string{"red", "green", "blue", "teal"}},
			{Name: "out", Identifiers: []string{"--out"}, Type: "string"},
			{Name: "token", Identifiers: []string{"--token"}, Type: "string", Secret: true},
		},
		Arguments: []ArgDef{{Name: "name", Type: "string", Default: "world"}},
	}
}

// ovLayers acquires the four standard layers in conventional precedence order
// (defaults < files < env < argv), failing the test on any acquisition error.
// TestCollect pins the one-liner (ergonomics E4): every channel reconciled in
// one call, equivalent to Binder.Bind AND to CollectP's overlaid layers; the
// P variant adds the provenance Report (closing the E2 audit's finding 3 —
// one-call and where-did-this-come-from compose now).
func TestCollect(t *testing.T) {
	cfg := writeConfig(t, "api:\n  endpoint: from-file\n  token: from-file-token\n")
	meta := BindMeta{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}
	t.Setenv("REGION", "from-env")

	newRtx := func() *Context {
		rtx := NewContextFor(tbDef(), []string{"--verbose"})
		rtx.WithBindMeta(meta)
		return rtx
	}

	got, err := Collect[tbInputs](newRtx())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !got.App.Flags.Verbose || got.App.Env.Region != "from-env" || got.App.Config.Endpoint != "from-file" {
		t.Errorf("Collect = %+v, want argv+env+file values reconciled", got.App)
	}

	// Equivalence 1: Collect == Binder.Bind.
	var bound tbInputs
	if err := NewBinder(meta).Bind(newRtx(), &bound); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if !reflect.DeepEqual(got, bound) {
		t.Errorf("Collect != Bind:\n collect=%+v\n bind=%+v", got, bound)
	}

	// Equivalence 2: Collect == CollectP's merged value — plus the Report.
	merged, report, err := CollectP[tbInputs](newRtx())
	if err != nil {
		t.Fatalf("CollectP: %v", err)
	}
	if !reflect.DeepEqual(got, merged) {
		t.Errorf("Collect != CollectP:\n collect=%+v\n collectP=%+v", got, merged)
	}
	if win, ok := report.Winner("App.Env.Region"); !ok || win.Layer != "env" || win.Raw != "from-env" {
		t.Errorf("Winner(Region) = %+v ok=%v, want env/from-env", win, ok)
	}
	if win, ok := report.Winner("App.Config.Endpoint"); !ok || win.Layer != "files" {
		t.Errorf("Winner(Endpoint) = %+v ok=%v, want the files layer", win, ok)
	}

	// Validation parity: a required config value missing errors in BOTH forms.
	bare := writeConfig(t, "api:\n  endpoint: only\n") // api.token (required) absent
	bareMeta := BindMeta{ConfigFiles: []ConfigFile{{Name: "app", Path: bare, Format: "yaml"}}}
	rtx := NewContextFor(tbDef(), nil)
	rtx.WithBindMeta(bareMeta)
	if _, err := Collect[tbInputs](rtx); err == nil {
		t.Error("Collect with missing required config = nil error, want loud")
	}
	rtx2 := NewContextFor(tbDef(), nil)
	rtx2.WithBindMeta(bareMeta)
	if _, _, err := CollectP[tbInputs](rtx2); err == nil {
		t.Error("CollectP with missing required config = nil error, want loud")
	}
}

func ovLayers(t *testing.T, rtx *Context, meta BindMeta) []Layer[ovInputs] {
	t.Helper()
	rtx.WithBindMeta(meta) // the channel functions derive their meta from the Context
	defaults, err := Defaults[ovInputs](rtx)
	if err != nil {
		t.Fatalf("Defaults: %v", err)
	}
	files, err := ParseFiles[ovInputs](rtx)
	if err != nil {
		t.Fatalf("ParseFiles: %v", err)
	}
	env, err := ParseEnv[ovInputs](rtx)
	if err != nil {
		t.Fatalf("ParseEnv: %v", err)
	}
	argv, err := ParseArgv[ovInputs](rtx)
	if err != nil {
		t.Fatalf("ParseArgv: %v", err)
	}
	return []Layer[ovInputs]{defaults, files, env, argv}
}

// PREC-02: each layer beats everything below it, and the Report names the
// winner. The same four layers are overlaid in every case; only the supplied
// sources vary.
func TestOverlay_precedence(t *testing.T) {
	cfg := writeConfig(t, "app:\n  color: red\n")
	meta := BindMeta{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}

	cases := []struct {
		name      string
		argv      []string
		env       string // APP_COLOR; "" = leave unset
		withFiles bool
		want      string
		winner    string
	}{
		{"argv wins over all", []string{"--color", "green"}, "teal", true, "green", "argv"},
		{"env over files and default", nil, "teal", true, "teal", "env"},
		{"files over default", nil, "", true, "red", "files"},
		{"default when no source", nil, "", false, "blue", "defaults"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.env != "" {
				t.Setenv("APP_COLOR", c.env)
			}
			m := BindMeta{}
			if c.withFiles {
				m = meta
			}
			rtx := NewContextFor(ovDef(), c.argv)
			got, rep := OverlayInputsP(ovLayers(t, rtx, m)...)
			if got.App.Flags.Color != c.want {
				t.Errorf("Color = %q, want %q", got.App.Flags.Color, c.want)
			}
			win, ok := rep.Winner("App.Flags.Color")
			if !ok || win.Layer != c.winner {
				t.Errorf("Winner(App.Flags.Color) = %+v (ok=%v), want layer %q", win, ok, c.winner)
			}
			if err := rep.Validate(); err != nil {
				t.Errorf("Validate: %v", err)
			}
		})
	}
}

// PREC-01: with no input at all, the defaults layer alone reproduces every
// declared default — and says so in the provenance.
func TestOverlay_defaultsOnly(t *testing.T) {
	rtx := NewContextFor(ovDef(), nil)
	got, rep := OverlayInputsP(ovLayers(t, rtx, BindMeta{})...)

	if got.App.Flags.Color != "blue" {
		t.Errorf("Color = %q, want blue (FlagDef default)", got.App.Flags.Color)
	}
	if got.App.Arguments.Name != "world" {
		t.Errorf("Name = %q, want world (ArgDef default)", got.App.Arguments.Name)
	}
	if got.App.Config.Retries != 3 {
		t.Errorf("Retries = %d, want 3 (recon default= tag)", got.App.Config.Retries)
	}
	for path, raw := range map[FieldPath]string{
		"App.Flags.Color":    "blue",
		"App.Arguments.Name": "world",
		"App.Config.Retries": "3",
	} {
		win, ok := rep.Winner(path)
		if !ok || win.Layer != "defaults" || win.Raw != raw {
			t.Errorf("Winner(%s) = %+v (ok=%v), want defaults/%q", path, win, ok, raw)
		}
	}
	if err := rep.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

// PREC-03: a layer that doesn't set a field never clobbers a lower layer's
// value — disjoint fields from different layers all survive the merge.
func TestOverlay_skipNotZero(t *testing.T) {
	cfg := writeConfig(t, "app:\n  retries: 5\n")
	meta := BindMeta{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}
	t.Setenv("REGION", "us-west")

	rtx := NewContextFor(ovDef(), []string{"--out", "report.txt"})
	got, rep := OverlayInputsP(ovLayers(t, rtx, meta)...)

	if got.App.Flags.Out != "report.txt" { // argv only
		t.Errorf("Out = %q, want report.txt", got.App.Flags.Out)
	}
	if got.App.Env.Region != "us-west" { // env only
		t.Errorf("Region = %q, want us-west", got.App.Env.Region)
	}
	if got.App.Config.Retries != 5 { // files only (beats the recon default)
		t.Errorf("Retries = %d, want 5", got.App.Config.Retries)
	}
	if got.App.Flags.Color != "blue" { // untouched by any higher layer
		t.Errorf("Color = %q, want blue", got.App.Flags.Color)
	}
	for path, layer := range map[FieldPath]string{
		"App.Flags.Out":      "argv",
		"App.Env.Region":     "env",
		"App.Config.Retries": "files",
	} {
		if win, ok := rep.Winner(path); !ok || win.Layer != layer {
			t.Errorf("Winner(%s) = %+v (ok=%v), want layer %q", path, win, ok, layer)
		}
	}
}

// Overlaying an empty layer is the identity, and a zero Report validates clean.
func TestOverlay_identity(t *testing.T) {
	rtx := NewContextFor(ovDef(), []string{"--color", "green"})
	layers := ovLayers(t, rtx, BindMeta{})

	base := OverlayInputs(layers...)
	withEmpty := OverlayInputs(append(layers, Layer[ovInputs]{Name: "empty"})...)
	if !reflect.DeepEqual(base, withEmpty) {
		t.Errorf("appending an empty layer changed the merge:\n base %+v\n with %+v", base, withEmpty)
	}

	var none ovInputs
	got, rep := OverlayInputsP[ovInputs]()
	if !reflect.DeepEqual(got, none) {
		t.Errorf("OverlayInputsP() = %+v, want the zero value", got)
	}
	if err := rep.Validate(); err != nil {
		t.Errorf("zero-layer Report.Validate = %v, want nil", err)
	}
}

// A hand-built layer participates in the merge and the provenance purely
// through its Presence — fields it holds but doesn't declare are ignored.
func TestOverlay_handBuiltLayer(t *testing.T) {
	rtx := NewContextFor(ovDef(), nil)
	layers := ovLayers(t, rtx, BindMeta{})

	var custom ovInputs
	custom.App.Flags.Color = "teal"
	custom.App.Flags.Out = "ignored.txt" // present in Values but NOT in Set
	override := Layer[ovInputs]{
		Name:   "test-override",
		Values: custom,
		Set:    Presence{"App.Flags.Color": {Layer: "test-override", Raw: "teal"}},
	}

	got, rep := OverlayInputsP(append(layers, override)...)
	if got.App.Flags.Color != "teal" {
		t.Errorf("Color = %q, want teal (hand-built layer)", got.App.Flags.Color)
	}
	if got.App.Flags.Out != "" {
		t.Errorf("Out = %q, want empty — undeclared fields must not leak from a hand-built layer", got.App.Flags.Out)
	}
	if win, _ := rep.Winner("App.Flags.Color"); win.Layer != "test-override" {
		t.Errorf("Winner layer = %q, want test-override", win.Layer)
	}
	if err := rep.Validate(); err != nil { // teal is in the enum
		t.Errorf("Validate: %v", err)
	}
}

// The Report's history records every layer that set a field, low → high, with
// the winner last; Fields enumerates everything any layer set, sorted.
func TestOverlay_report(t *testing.T) {
	cfg := writeConfig(t, "app:\n  color: red\n")
	meta := BindMeta{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}
	t.Setenv("APP_COLOR", "teal")

	rtx := NewContextFor(ovDef(), []string{"--color", "green"})
	_, rep := OverlayInputsP(ovLayers(t, rtx, meta)...)

	hist := rep.History("App.Flags.Color")
	var layers []string
	for _, p := range hist {
		layers = append(layers, p.Layer)
	}
	if want := []string{"defaults", "files", "env", "argv"}; !reflect.DeepEqual(layers, want) {
		t.Errorf("History layers = %v, want %v", layers, want)
	}
	if hist[len(hist)-1].Raw != "green" {
		t.Errorf("winning Raw = %q, want green", hist[len(hist)-1].Raw)
	}

	fields := rep.Fields()
	for i := 1; i < len(fields); i++ {
		if fields[i-1] >= fields[i] {
			t.Fatalf("Fields not sorted: %v", fields)
		}
	}
	if _, ok := rep.Winner("App.Flags.Color"); !ok {
		t.Error("Winner(App.Flags.Color) not found")
	}
	if _, ok := rep.Winner("App.Flags.Nope"); ok {
		t.Error("Winner of a never-set path reported ok")
	}
	if rep.History("App.Flags.Nope") != nil {
		t.Error("History of a never-set path should be nil")
	}
}

// A secret input's raw value never appears in provenance, whichever layer
// supplied it.
func TestOverlay_secretRedaction(t *testing.T) {
	t.Setenv("APP_TOKEN", "env-sk-456") // SNAKE_UPPER of recon key "app.token"
	rtx := NewContextFor(ovDef(), []string{"--token", "argv-sk-123"})
	_, rep := OverlayInputsP(ovLayers(t, rtx, BindMeta{})...)

	for _, p := range rep.History("App.Flags.Token") {
		if strings.Contains(p.Raw, "sk-") {
			t.Errorf("secret leaked in %s provenance: %q", p.Layer, p.Raw)
		}
		if p.Raw != "[redacted]" {
			t.Errorf("%s Raw = %q, want [redacted]", p.Layer, p.Raw)
		}
	}
	if win, _ := rep.Winner("App.Flags.Token"); win.Layer != "argv" {
		t.Errorf("Winner layer = %q, want argv", win.Layer)
	}
}

// Required-flag fixture: required, recon-keyed, no default — satisfiable from
// any layer, which only the post-overlay Validate can know.
type ovReqInputs struct {
	App struct {
		Flags struct {
			Token string `rotini:"token" recon:"api.token"`
		}
		Arguments struct{}
	}
}

func ovReqDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "token", Identifiers: []string{"--token"}, Type: "string", Required: true}},
	}
}

func TestOverlay_validateRequiredAcrossLayers(t *testing.T) {
	acquire := func(t *testing.T) (ovReqInputs, Report) {
		t.Helper()
		rtx := NewContextFor(ovReqDef(), nil) // never on argv; no meta to bind
		defaults, err := Defaults[ovReqInputs](rtx)
		if err != nil {
			t.Fatalf("Defaults: %v", err)
		}
		env, err := ParseEnv[ovReqInputs](rtx)
		if err != nil {
			t.Fatalf("ParseEnv: %v", err)
		}
		argv, err := ParseArgv[ovReqInputs](rtx)
		if err != nil {
			t.Fatalf("ParseArgv: %v", err)
		}
		return OverlayInputsP(defaults, env, argv)
	}

	t.Run("satisfied by the env layer", func(t *testing.T) {
		t.Setenv("API_TOKEN", "from-env")
		got, rep := acquire(t)
		if got.App.Flags.Token != "from-env" {
			t.Errorf("Token = %q, want from-env", got.App.Flags.Token)
		}
		if err := rep.Validate(); err != nil {
			t.Errorf("Validate = %v, want nil — required satisfied by a lower layer", err)
		}
	})

	t.Run("missing everywhere", func(t *testing.T) {
		_, rep := acquire(t)
		err := rep.Validate()
		if err == nil || !strings.Contains(err.Error(), "token") {
			t.Errorf("Validate = %v, want a missing-required error naming token", err)
		}
	})
}

// An out-of-enum value supplied by a lower layer (here: a config file) is
// caught by the merged validation, exactly like Binder.Bind's locus.
func TestOverlay_validateEnumOnFileSuppliedFlag(t *testing.T) {
	cfg := writeConfig(t, "app:\n  color: magenta\n") // not in the enum
	meta := BindMeta{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}

	rtx := NewContextFor(ovDef(), nil)
	got, rep := OverlayInputsP(ovLayers(t, rtx, meta)...)
	if got.App.Flags.Color != "magenta" {
		t.Fatalf("Color = %q, want magenta supplied by the files layer", got.App.Flags.Color)
	}
	err := rep.Validate()
	if err == nil || !strings.Contains(err.Error(), "color") {
		t.Errorf("Validate = %v, want an enum violation naming color", err)
	}
}

// The stdin layer decodes the leaf payload and records its presence.
func TestOverlay_stdinLayer(t *testing.T) {
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
	rtx.Stdin = strings.NewReader("kind: Widget\nname: foo\n")

	stdin, err := ParseStdin[tbStdinInputs](rtx)
	if err != nil {
		t.Fatalf("ParseStdin: %v", err)
	}
	if win, ok := stdin.Set["App.Stdin"]; !ok || win.Layer != "stdin" || win.Raw != "" {
		t.Errorf("Set[App.Stdin] = %+v (ok=%v), want stdin layer with empty Raw", win, ok)
	}

	got := OverlayInputs(stdin)
	if got.App.Stdin == nil || got.App.Stdin.Kind != "Widget" || got.App.Stdin.Name != "foo" {
		t.Errorf("Stdin = %+v, want {Widget foo}", got.App.Stdin)
	}

	// No piped input: nothing decoded, nothing recorded.
	rtx2 := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
	rtx2.Stdin = strings.NewReader("")
	empty, err := ParseStdin[tbStdinInputs](rtx2)
	if err != nil {
		t.Fatalf("ParseStdin(empty): %v", err)
	}
	if len(empty.Set) != 0 {
		t.Errorf("empty stdin recorded presence: %v", empty.Set)
	}
}

// Binder.Bind and the à-la-carte overlay are two paths over the same
// machinery: on identical inputs they must produce identical structs.
func TestOverlay_bindEquivalence(t *testing.T) {
	cfg := writeConfig(t, "app:\n  token: cfg-secret\n  retries: 5\n")
	meta := BindMeta{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}
	t.Setenv("REGION", "eu-central")

	cases := []struct {
		name string
		argv []string
	}{
		{"argv supplied", []string{"--color", "green", "--out", "report.txt", "alice"}},
		{"defaults fill the gaps", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var want ovInputs
			if err := NewBinder(meta).Bind(NewContextFor(ovDef(), c.argv), &want); err != nil {
				t.Fatalf("Bind: %v", err)
			}

			rtx := NewContextFor(ovDef(), c.argv)
			got, rep := OverlayInputsP(ovLayers(t, rtx, meta)...)
			if err := rep.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("overlay ≠ Bind:\n overlay %+v\n bind    %+v", got, want)
			}
		})
	}
}

// Variadic fixture: a fixed positional followed by a trailing []string that
// absorbs the rest, the shape codegen emits for a variadic argument.
type ovVarInputs struct {
	App struct {
		Flags     struct{}
		Arguments struct {
			First string   `rotini:"first"`
			Rest  []string `rotini:"rest"`
		}
	}
}

func ovVarDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Arguments: []ArgDef{
			{Name: "first", Type: "string"},
			{Name: "rest", Type: "string", Variadic: true},
		},
	}
}

func TestOverlay_edgeCases(t *testing.T) {
	t.Run("nil context is a ParseError", func(t *testing.T) {
		if _, err := ParseArgv[ovInputs](nil); err == nil {
			t.Error("ParseArgv(nil context) = nil error, want a ParseError")
		}
		if _, err := Defaults[ovInputs](nil); err == nil {
			t.Error("Defaults(nil context) = nil error, want a ParseError")
		}
	})

	t.Run("required env input fails in-channel", func(t *testing.T) {
		type reqEnvInputs struct {
			App struct {
				Flags     struct{}
				Arguments struct{}
				Env       struct {
					Region string `rotini:"region" recon:"ov_required_region,required"`
				}
			}
		}
		rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
		if _, err := ParseEnv[reqEnvInputs](rtx); err == nil {
			t.Error("ParseEnv with a missing required env input = nil error, want the channel's error")
		}
	})

	t.Run("stale hand-built path copies nothing", func(t *testing.T) {
		var vals ovInputs
		vals.App.Flags.Color = "teal"
		got := OverlayInputs(Layer[ovInputs]{
			Name:   "stale",
			Values: vals,
			Set:    Presence{"App.Nope.Color": {Layer: "stale"}, "App.Flags.Color.Deeper": {Layer: "stale"}},
		})
		var zero ovInputs
		if !reflect.DeepEqual(got, zero) {
			t.Errorf("stale paths copied values: %+v", got)
		}
	})

	t.Run("variadic argument presence", func(t *testing.T) {
		rtx := NewContextFor(ovVarDef(), []string{"alpha", "beta", "gamma"})
		argv, err := ParseArgv[ovVarInputs](rtx)
		if err != nil {
			t.Fatalf("ParseArgv: %v", err)
		}
		if argv.Values.App.Arguments.First != "alpha" || len(argv.Values.App.Arguments.Rest) != 2 {
			t.Errorf("bound = %+v, want alpha + [beta gamma]", argv.Values.App.Arguments)
		}
		if win, ok := argv.Set["App.Arguments.Rest"]; !ok || win.Raw != "beta, gamma" {
			t.Errorf("Set[App.Arguments.Rest] = %+v (ok=%v), want raw \"beta, gamma\"", win, ok)
		}
	})
}

// ExampleOverlayInputsP shows the merge and provenance contract with two
// hand-built layers: order is precedence, and the Report names the winner.
func ExampleOverlayInputsP() {
	type inputs struct {
		App struct {
			Flags struct {
				Color string `rotini:"color"`
			}
		}
	}
	var defaults, env inputs
	defaults.App.Flags.Color = "blue"
	env.App.Flags.Color = "teal"

	merged, report := OverlayInputsP(
		Layer[inputs]{Name: "defaults", Values: defaults, Set: Presence{"App.Flags.Color": {Layer: "defaults", Raw: "blue"}}},
		Layer[inputs]{Name: "env", Values: env, Set: Presence{"App.Flags.Color": {Layer: "env", Raw: "teal"}}},
	)
	win, _ := report.Winner("App.Flags.Color")
	fmt.Println(merged.App.Flags.Color, "from", win.Layer)
	// Output: teal from env
}

// The env and files layers bind a flag's fallback exactly as the Binder does — they share
// bindFlagFallback. Each case here was wrong on this path too: a config list bound as the one
// string "[a b]", an env list ignored its separator, and a bad value was silently dropped.
func TestOverlay_flagFallbacksMatchTheBinder(t *testing.T) {
	cfg := writeConfig(t, "tags: [a, b]\nlabels: {k: v}\n")
	meta := BindMeta{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}

	rtx := NewContextFor(tbListDef(), nil)
	rtx.WithBindMeta(meta)
	files, err := ParseFiles[tbListInputs](rtx)
	if err != nil {
		t.Fatalf("ParseFiles: %v", err)
	}
	if f := files.Values.App.Flags; !slices.Equal(f.Tags, []string{"a", "b"}) || f.Labels["k"] != "v" {
		t.Errorf("files layer: tags=%q labels=%v", f.Tags, f.Labels)
	}

	t.Setenv("PORTS", "1,2")
	env, err := ParseEnv[tbListInputs](rtx)
	if err != nil || !slices.Equal(env.Values.App.Flags.Ports, []int{1, 2}) {
		t.Errorf("env layer: ports=%v err=%v", env.Values.App.Flags.Ports, err)
	}

	t.Setenv("PORTS", "1,x")
	if _, err := ParseEnv[tbListInputs](rtx); err == nil || !strings.Contains(err.Error(), "environment variable PORTS") {
		t.Errorf("env layer with a bad value: err = %v, want it to name PORTS", err)
	}
}

// Collect and the per-channel layer functions must agree about which frames a struct describes.
//
// They did not, for one release of this work: the anchor moved from the leaf to the caller's own
// frame everywhere, but the fit check that came with it was added only to Collect and CollectP.
// ParseArgv, Defaults, ParseEnv and ParseFiles went on accepting a struct that could not describe
// the running command and returning it zeroed with a nil error — the same silent failure the
// anchor work existed to remove, left in the corner of the same API.
//
// These are the tests that would have caught that, so they assert the whole family together
// rather than one function.

type acFlags struct {
	Own  bool `rotini:"own"`
	Help bool `rotini:"help"`
}
type acCmd struct {
	Flags     acFlags
	Arguments struct{}
}

// acDeep describes three commands. The root's hook is one deep, so it can never be right there.
type acDeep struct {
	A acCmd
	B acCmd
	C acCmd
}

// acOwn describes one command — what a root hook legitimately collects.
type acOwn struct{ Root acCmd }

func acDefs(own string) []FlagDef {
	return []FlagDef{
		{Name: own, Identifiers: []string{"--" + own}, Type: "bool"},
		{Name: "help", Identifiers: []string{"-h", "--help"}, Type: "bool"},
	}
}

func acDef() Definition {
	return Definition{
		Name: "root", Handler: "Root", Flags: acDefs("own"),
		Commands: []CommandDef{{Name: "leaf", Handler: "Leaf", Flags: acDefs("leafown")}},
	}
}

type acProg struct{ inRootHook func(*Context) }

func (p acProg) Root() Handlers { return acRootH{probe: p.inRootHook} }
func (p acProg) Leaf() Handlers { return acNoop{} }

type acNoop struct{ DefaultHooks }

func (acNoop) Run(context.Context, *Context) {}

type acRootH struct {
	DefaultPreRun
	DefaultPostRun
	DefaultCascadingPostRun
	probe func(*Context)
}

func (h acRootH) Run(context.Context, *Context) {}
func (h acRootH) CascadingPreRun(_ context.Context, rtx *Context) {
	if h.probe != nil {
		h.probe(rtx)
	}
}

// acEntryPoints is every public way to acquire inputs, so a new one cannot be added without
// deciding what it does here.
func acEntryPoints(rtx *Context) map[string]error {
	_, ec := Collect[acDeep](rtx)
	_, _, ep := CollectP[acDeep](rtx)
	_, ea := ParseArgv[acDeep](rtx)
	_, ed := Defaults[acDeep](rtx)
	_, ee := ParseEnv[acDeep](rtx)
	_, ef := ParseFiles[acDeep](rtx)
	return map[string]error{
		"Collect": ec, "CollectP": ep, "ParseArgv": ea,
		"Defaults": ed, "ParseEnv": ee, "ParseFiles": ef,
	}
}

// TestAnchor_everyEntryPointRejectsATooDeepType is the regression. A struct describing more
// commands than the caller is deep cannot be describing the caller, and every entry point has to
// say so rather than hand back zeros.
func TestAnchor_everyEntryPointRejectsATooDeepType(t *testing.T) {
	var got map[string]error
	p := NewProgram(acDef(), acProg{inRootHook: func(rtx *Context) { got = acEntryPoints(rtx) }}).
		WithStdout(io.Discard).WithStderr(io.Discard)
	p.Run([]string{"--own", "leaf"})

	if got == nil {
		t.Fatal("the root's cascading hook never ran")
	}
	for name, err := range got {
		if err == nil {
			t.Errorf("%s accepted a 3-command type from a hook 1 command deep, silently", name)
			continue
		}
		if !strings.Contains(err.Error(), "acDeep") || !strings.Contains(err.Error(), "ITS OWN") {
			t.Errorf("%s rejected it, but not with the shared explanation: %v", name, err)
		}
	}
}

// TestAnchor_everyEntryPointAcceptsTheCallersOwnType is the other half: the check must not fire
// on the type a hook is supposed to collect, in any of the six.
func TestAnchor_everyEntryPointAcceptsTheCallersOwnType(t *testing.T) {
	var errs map[string]error
	var own bool
	p := NewProgram(acDef(), acProg{inRootHook: func(rtx *Context) {
		in, ec := Collect[acOwn](rtx)
		own = in.Root.Flags.Own
		_, _, ep := CollectP[acOwn](rtx)
		_, ea := ParseArgv[acOwn](rtx)
		_, ed := Defaults[acOwn](rtx)
		_, ee := ParseEnv[acOwn](rtx)
		_, ef := ParseFiles[acOwn](rtx)
		errs = map[string]error{
			"Collect": ec, "CollectP": ep, "ParseArgv": ea,
			"Defaults": ed, "ParseEnv": ee, "ParseFiles": ef,
		}
	}}).WithStdout(io.Discard).WithStderr(io.Discard)
	p.Run([]string{"--own", "leaf"})

	for name, err := range errs {
		if err != nil {
			t.Errorf("%s rejected the hook's own inputs type: %v", name, err)
		}
	}
	if !own {
		t.Error("Collect did not read the root's own --own from its cascading hook")
	}
}

// Collect and CollectP are two code paths to one answer: Collect goes through [Binder.Bind],
// CollectP acquires five layers separately and overlays them. TestCollect pins that they agree
// for one argv shape. These pin that they agree across the whole precedence matrix — and, which
// nothing covered, that they FAIL the same way.
//
// The failure half matters most. A handler reaches for CollectP when it wants provenance, often
// after starting with Collect; if the two disagreed on which inputs are legal, that swap would
// change behaviour while looking like it only added a Report.

func describeErr(err error) string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%T: %s", err, err.Error())
}

func TestCollect_andCollectPAgreeAcrossThePrecedenceMatrix(t *testing.T) {
	cfg := writeConfig(t, "app:\n  color: red\n")
	withFiles := BindMeta{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}

	for _, c := range []struct {
		name string
		argv []string
		env  string // APP_COLOR; "" leaves it unset
		meta BindMeta
		want string // merged Color, "" when the case is an error
	}{
		{"argv beats everything", []string{"--color", "green"}, "teal", withFiles, "green"},
		{"env beats files and default", nil, "teal", withFiles, "teal"},
		{"files beat the default", nil, "", withFiles, "red"},
		{"the default when nothing supplies", nil, "", BindMeta{}, "blue"},
		{"an enum violation on argv", []string{"--color", "mauve"}, "", withFiles, ""},
		{"an enum violation from env", nil, "mauve", withFiles, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.env != "" {
				t.Setenv("APP_COLOR", c.env)
			}
			mk := func() *Context {
				rtx := NewContextFor(ovDef(), c.argv)
				rtx.WithBindMeta(c.meta)
				return rtx
			}

			viaCollect, errCollect := Collect[ovInputs](mk())
			viaCollectP, _, errCollectP := CollectP[ovInputs](mk())

			// The verdict must be identical: swapping Collect for CollectP to gain provenance
			// must not change which inputs are legal.
			if describeErr(errCollect) != describeErr(errCollectP) {
				t.Errorf("the two paths disagree on failure:\n  Collect  → %s\n  CollectP → %s",
					describeErr(errCollect), describeErr(errCollectP))
			}

			// On SUCCESS the values must be identical too.
			if errCollect == nil && !reflect.DeepEqual(viaCollect, viaCollectP) {
				t.Errorf("the two paths disagree on values:\n  Collect  → %+v\n  CollectP → %+v",
					viaCollect.App, viaCollectP.App)
			}

			// On FAILURE they deliberately differ, and the difference is pinned rather than
			// left to be rediscovered: Collect stops at the first argv problem, before the env
			// and config channels are read, so a config-supplied default (Retries, recon
			// default=3) is absent from its partial value and present in CollectP's.
			if errCollect != nil {
				if viaCollect.App.Config.Retries != 0 {
					t.Errorf("Collect's partial value gained a config default it should not have read: %d",
						viaCollect.App.Config.Retries)
				}
				if viaCollectP.App.Config.Retries != 3 {
					t.Errorf("CollectP's merged value lost the config default: %d", viaCollectP.App.Config.Retries)
				}
			}
			if c.want != "" {
				if errCollect != nil {
					t.Fatalf("Collect: %v", errCollect)
				}
				if got := viaCollect.App.Flags.Color; got != c.want {
					t.Errorf("Color = %q, want %q", got, c.want)
				}
			} else if errCollect == nil {
				t.Error("an enum violation was accepted")
			}
		})
	}
}

// TestCollect_isIdempotent: a handler that collects twice — or a helper that collects for
// itself — must get the same answer, since the Context is shared across a run's hooks.
func TestCollect_isIdempotent(t *testing.T) {
	cfg := writeConfig(t, "app:\n  color: red\n")
	rtx := NewContextFor(ovDef(), []string{"--color", "green"})
	rtx.WithBindMeta(BindMeta{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}})

	first, err1 := Collect[ovInputs](rtx)
	second, err2 := Collect[ovInputs](rtx)
	if err1 != nil || err2 != nil {
		t.Fatalf("Collect: %v / %v", err1, err2)
	}
	if first != second {
		t.Errorf("Collect is not idempotent on one Context:\n  first  = %+v\n  second = %+v", first.App, second.App)
	}
}

// TestReport_validateChecksWhatWasSupplied pins the rule Validate's doc now states, because the
// two halves look the same from the outside and only one of them fires on an absent field.
func TestReport_validateChecksWhatWasSupplied(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "color", Identifiers: []string{"--color"}, Type: "string", Default: "blue",
				Enum: []string{"red", "green", "blue"}},
			{Name: "mode", Identifiers: []string{"--mode"}, Type: "string", Required: true,
				Enum: []string{"fast", "slow"}},
		},
	}
	type flags struct {
		Color string `rotini:"color"`
		Mode  string `rotini:"mode"`
	}
	type cmd struct {
		Flags     flags
		Arguments struct{}
	}
	type inputs struct{ App cmd }

	argvOnly, err := ParseArgv[inputs](NewContextFor(def, nil))
	if err != nil {
		t.Fatal(err)
	}

	// A PRESENCE rule fires on absence: --mode is required and nothing supplied it.
	merged, rep := OverlayInputsP(argvOnly)
	if e := rep.Validate(); e == nil {
		t.Error("a required input nobody supplied passed validation")
	}

	// A VALUE rule does not fire on absence: Color merged as "" — not in its enum — because no
	// layer claimed it. Dropping the defaults layer is what exposes this, which is why the doc
	// says to build custom precedence from all five channels.
	if merged.App.Flags.Color != "" {
		t.Fatalf("fixture no longer demonstrates the case: Color = %q", merged.App.Flags.Color)
	}
	for _, p := range rep.Fields() {
		if p == "App.Flags.Color" {
			t.Error("Color was reported as supplied by a layer, which it was not")
		}
	}

	// With the defaults layer present the field is supplied, and the enum applies again.
	defaults, err := Defaults[inputs](NewContextFor(def, nil))
	if err != nil {
		t.Fatal(err)
	}
	withDefaults, _ := OverlayInputsP(defaults, argvOnly)
	if withDefaults.App.Flags.Color != "blue" {
		t.Errorf("Color with defaults = %q, want the declared default", withDefaults.App.Flags.Color)
	}
}

// TestCollect_isCorrectInACascadingHookAtEveryDepth is the finding, fixed. The same call reads
// mid's own flag whether or not a sub-command was invoked, and whether mid's type spans its
// whole lineage (an ordinary cli) or only itself (a composed child).
//
// Before this, three of these four cells returned false with a nil error.
func TestCollect_isCorrectInACascadingHookAtEveryDepth(t *testing.T) {
	for _, argv := range [][]string{
		{"mid", "--midonly"},         // mid IS the leaf
		{"mid", "--midonly", "leaf"}, // mid is a MIDDLE frame
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			runF(t, argv, func(rtx *Context) {
				span, err := Collect[fMidSpan](rtx)
				if err != nil {
					t.Errorf("Collect[fMidSpan] (ordinary cli): %v", err)
				} else if !span.Mid.Flags.MidOnly {
					t.Error("Collect[fMidSpan] (ordinary cli) did not read mid's own --midonly")
				}

				own, err := Collect[fMidOwn](rtx)
				if err != nil {
					t.Errorf("Collect[fMidOwn] (composed child): %v", err)
				} else if !own.Mid.Flags.MidOnly {
					t.Error("Collect[fMidOwn] (composed child) did not read mid's own --midonly")
				}
			}, nil)
		})
	}
}

// TestCollect_doesNotSeeADescendantsFlag is the other half of correctness: anchoring on the
// caller's frame must not reach DOWN the chain either.
func TestCollect_doesNotSeeADescendantsFlag(t *testing.T) {
	runF(t, []string{"mid", "leaf", "--leafonly"}, func(rtx *Context) {
		own, err := Collect[fMidOwn](rtx)
		if err != nil {
			t.Fatal(err)
		}
		if own.Mid.Flags.MidOnly {
			t.Error("mid's frame reported --midonly, which was never passed")
		}
	}, nil)
}

// TestCollect_rejectsADescendantsType is the exact check the frame makes possible: a struct that
// describes more commands than the caller is deep cannot be describing the caller.
//
// Under leaf-anchoring this was silent — the struct did not fit, binding was skipped, and every
// field came back zero with a nil error.
func TestCollect_rejectsADescendantsType(t *testing.T) {
	var err error
	runF(t, []string{"mid", "leaf"}, nil, func(rtx *Context) {
		_, err = Collect[fLeafSpan](rtx) // 3 fields, but the root is 1 deep
	})
	if err == nil {
		t.Fatal("collecting a descendant's 3-field type from the root's hook returned no error")
	}
	for _, want := range []string{"fLeafSpan", "describes 3 commands", "only 1 deep", "ITS OWN"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %q:\n%s", want, err)
		}
	}
}

// TestCollect_leafRunIsUnchanged pins the compatibility claim: for a leaf hook the frame IS the
// leaf, so the anchor reduces to what it always was.
func TestCollect_leafRunIsUnchanged(t *testing.T) {
	var got fLeafSpan
	var err error
	p := NewProgram(fDef(), fLeafProbe{capture: func(rtx *Context) { got, err = Collect[fLeafSpan](rtx) }}).
		WithStdout(io.Discard).WithStderr(io.Discard)
	if _, e := p.Run([]string{"mid", "--midonly", "leaf", "--leafonly"}); e != nil {
		t.Fatal(e)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !got.Leaf.Flags.LeafOnly || !got.Mid.Flags.MidOnly {
		t.Errorf("leaf Run got leaf=%v mid=%v, want both true", got.Leaf.Flags.LeafOnly, got.Mid.Flags.MidOnly)
	}
}
