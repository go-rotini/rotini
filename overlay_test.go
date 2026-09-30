package rotini

import (
	"fmt"
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
