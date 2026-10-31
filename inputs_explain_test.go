package rotini

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestInputReportExplain(t *testing.T) {
	t.Parallel()
	rep := formatFixture(t, "--verbose", "deploy", "-e", "prod")
	var b strings.Builder
	must(t, rep.Explain(&b))
	want := `--verbose = "true" from the command line
ACME_HTTP__* = (set) from ACME_HTTP__*
ACME_REGION = "eu" from ACME_REGION
--env = "prod" from the command line (overrides "from-main" from acme.env in acme.yaml, "dev" from default)
--output = "from-project" from acme.output in .acme.yaml (overrides "table" from default)
`
	if b.String() != want {
		t.Errorf("Explain =\n%s\nwant\n%s", b.String(), want)
	}
}

func TestInputReportLabelAndSources(t *testing.T) {
	t.Parallel()
	rep := formatFixture(t, "deploy")
	tests := []struct {
		path    FieldPath
		label   string
		sources []string
	}{
		{"Acme.Flags.Verbose", "--verbose", []string{"--verbose"}},
		{"Deploy.Flags.Env", "--env", []string{"--env", "ACME_ENV", "acme.env", "default"}},
		{"Deploy.Flags.Labels", "--label", []string{"--label"}},
		{"Deploy.Env.Region", "ACME_REGION", []string{"ACME_REGION"}},
		{"Deploy.Env.HTTP", "ACME_HTTP__*", []string{"ACME_HTTP__*"}},
		{"Deploy.Flags.Nope", "", nil},
		{"Nope.Flags.Env", "", nil},
		{"Deploy", "", nil},
	}
	for _, tc := range tests {
		if got := rep.Label(tc.path); got != tc.label {
			t.Errorf("Label(%s) = %q, want %q", tc.path, got, tc.label)
		}
		if got := rep.Sources(tc.path); !slices.Equal(got, tc.sources) {
			t.Errorf("Sources(%s) = %q, want %q", tc.path, got, tc.sources)
		}
	}
}

// An argument, a config input pinned to one file, a key of the selected profile, a variable_file
// variable and an env prefix each read in the user's words.
func TestInputReportExplain_configProfileAndFiles(t *testing.T) {
	t.Parallel()
	type in struct {
		App struct {
			Flags struct {
				Profile string `rotini:"profile" recon:"profile" env:"APP_PROFILE"`
				Token   string `rotini:"token" recon:"token" env:"APP_TOKEN" envfile:"APP_TOKEN_FILE"`
			}
			Arguments struct {
				Target string `rotini:"target"`
			}
			Env struct {
				Region string `rotini:"region" recon:"region,default=us"`
			}
			Config struct {
				Host   string `rotini:"host" recon:"db.host,default=localhost"`
				Pinned string `rotini:"pinned" recon:"pinned" cfgfile:"app"`
			}
		}
	}
	dir := t.TempDir()
	app := pfWrite(t, dir, "app.yaml", pfFile)
	pfWrite(t, dir, "token.txt", "s3cret\n")
	meta := pfSettings(pfFileAt(app))
	meta.EnvPrefix = "APP"
	def := Definition{Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "profile", Identifiers: []string{"-p", "--profile"}, Type: "string"},
			{Name: "token", Identifiers: []string{"--token"}, Type: "string", Secret: true},
		},
		Arguments: []ArgDef{{Name: "target", Type: "string"}},
	}
	rtx := NewContextFor(def, []string{"--profile", "prod", "web"}).WithDir(dir).
		WithEnviron([]string{"APP_TOKEN_FILE=token.txt"}).WithInputSettings(meta)
	_, rep, err := rtx.InputsWithReport[in]()
	must(t, err)
	var b strings.Builder
	must(t, rep.Explain(&b))
	want := `<target> = "web" from the command line
db.host = "prod.example" from db.host in app.yaml (profile prod) (overrides "localhost" from default)
pinned = "prod-pin" from pinned in app.yaml (profile prod)
APP_REGION = "us" from default
--profile = "prod" from the command line
--token = [redacted] from the file APP_TOKEN_FILE names
`
	if b.String() != want {
		t.Errorf("Explain =\n%s\nwant\n%s", b.String(), want)
	}
	for path, sources := range map[FieldPath][]string{
		"App.Arguments.Target": {"<target>"},
		"App.Flags.Token":      {"--token", "APP_TOKEN", "the file APP_TOKEN_FILE names", "token"},
		"App.Env.Region":       {"APP_REGION", "default"},
		"App.Config.Host":      {"db.host", "default"},
		"App.Config.Pinned":    {"pinned in app.yaml"},
	} {
		if got := rep.Sources(path); !slices.Equal(got, sources) {
			t.Errorf("Sources(%s) = %q, want %q", path, got, sources)
		}
	}
	if got := rep.Label("App.Flags.Profile"); got != "--profile" {
		t.Errorf("Label of a flag whose short form comes first = %q, want --profile", got)
	}
}

// A report with no rotini layer names nothing: Explain falls back to field paths and layer names.
func TestInputReportExplain_handBuiltOnly(t *testing.T) {
	t.Parallel()
	type in struct {
		App struct{ Flags struct{ Name string } }
	}
	_, rep := MergeInputsWithReport(InputLayer[in]{Name: "prompt", Set: Presence{"App.Flags.Name": {Raw: "ada"}}})
	var b strings.Builder
	must(t, rep.Explain(&b))
	if want := "App.Flags.Name = \"ada\" from prompt\n"; b.String() != want {
		t.Errorf("Explain = %q, want %q", b.String(), want)
	}
	if rep.Label("App.Flags.Name") != "" || rep.Sources("App.Flags.Name") != nil {
		t.Errorf("a hand-built report named an input: %q %q", rep.Label("App.Flags.Name"), rep.Sources("App.Flags.Name"))
	}
}

func TestInputReportExplain_emptyAndWriteError(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	if err := (InputReport{}).Explain(&b); err != nil || b.Len() != 0 {
		t.Errorf("empty report wrote %q, %v", b.String(), err)
	}
	if (InputReport{}).Label("A.Flags.B") != "" || (InputReport{}).Sources("A.Flags.B") != nil {
		t.Error("the zero report named an input")
	}
	rep := formatFixture(t, "deploy")
	if err := rep.Explain(failingWriter{err: errors.New("boom")}); err == nil || !strings.Contains(err.Error(), "write the input report") {
		t.Errorf("Explain to a failing writer = %v", err)
	}
}

// A value error names an author's own type as a value and rotini's or Go's by what it is.
func TestValueLabel(t *testing.T) {
	t.Parallel()
	for typ, want := range map[reflect.Type]string{
		reflect.TypeFor[textID]():    "value",
		reflect.TypeFor[*textID]():   "value",
		reflect.TypeFor[ByteSize]():  "size",
		reflect.TypeFor[int]():       "int",
		reflect.TypeFor[[]string]():  "[]string",
		reflect.TypeFor[time.Time](): "time",
	} {
		if got := valueLabel(typ, noFeatures); got != want {
			t.Errorf("valueLabel(%s) = %q, want %q", typ, got, want)
		}
	}
	for typ, want := range map[reflect.Type]string{
		reflect.TypeFor[int8]():    "integer",
		reflect.TypeFor[uint16]():  "non-negative integer",
		reflect.TypeFor[float32](): "number",
	} {
		if got := numberLabel(typ); got != want {
			t.Errorf("numberLabel(%s) = %q, want %q", typ, got, want)
		}
	}
}

// textID is an author's own TextUnmarshaler.
type textID string

func (t *textID) UnmarshalText(b []byte) error { *t = textID(b); return nil }
