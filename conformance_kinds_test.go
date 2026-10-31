package rotini

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The kind rows of the input conformance matrix: every rotini error a read of each channel
// returns says what went wrong in its Kind, so a reporter can branch on it as it does on the
// command line's.

type kdInputs struct {
	App struct {
		Flags struct {
			Port int `rotini:"port" recon:"port" env:"KD_PORT"`
		}
		Arguments struct{}
		Env       struct {
			Mode  string `rotini:"mode" recon:"mode" env:"KD_MODE" enum:"[\"fast\",\"slow\"]"`
			Count int    `rotini:"count" recon:"count" env:"KD_COUNT"`
		}
		Config struct {
			Replicas int `rotini:"replicas" recon:"replicas"`
		}
	}
}

type kdRequiredInputs struct {
	App struct {
		Flags     struct{}
		Arguments struct{}
		Env       struct {
			Token string `rotini:"token" recon:"token,required" env:"KD_TOKEN"`
		}
	}
}

type kdStdinInputs struct {
	App struct {
		Flags     struct{}
		Arguments struct{}
		Stdin     *AppStdin `stdin:"json,required"`
	}
}

func kdDef() Definition {
	return Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "port", Identifiers: []string{"--port"}, Type: "int"},
	}}
}

// kdRead reads the inputs T for argv from env, stdin and the configuration file body (none
// when body is ""), checked against schema when it is set.
func kdRead[T any](t *testing.T, env []string, stdin, body, schema string, argv ...string) error {
	t.Helper()
	dir := t.TempDir()
	var meta InputSettings
	if body != "" {
		path := filepath.Join(dir, "app.yaml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		meta.ConfigFiles = []ConfigFileDef{{Name: "app", Path: path, Format: "yaml", Schema: schema}}
	}
	rtx := NewContextFor(kdDef(), argv).WithDir(dir).WithEnviron(env).WithStdin(strings.NewReader(stdin))
	var in T
	return NewInputReader(meta).Read(rtx, &in)
}

// wantKind fails unless err is an *InputError of kind want, read from channel, against the
// command app, and every *InputError and *ParseError in err's tree has a kind.
func wantKind(t *testing.T, err error, channel string, want InputErrorKind) {
	t.Helper()
	ie, ok := asError[*InputError](err)
	if !ok {
		t.Fatalf("err = %v (%T), want an *InputError", err, err)
	}
	if ie.Kind != want || ie.Channel != channel || ie.Command != "app" {
		t.Errorf("err = %v: kind %v, channel %q, command %q; want %v, %q, app", err, ie.Kind, ie.Channel, ie.Command, want, channel)
	}
	if ie.usage && ie.Path() != "app" {
		t.Errorf("Path() = %q, want app", ie.Path())
	}
	assertKinded(t, err)
}

// assertKinded fails for any *InputError or *ParseError in err's tree with no kind.
func assertKinded(t *testing.T, err error) {
	t.Helper()
	switch e := err.(type) {
	case nil:
		return
	case *InputError:
		if e.Kind == InputErrorKindUnspecified {
			t.Errorf("%q: an *InputError with no kind", e.Error())
		}
	case *ParseError:
		if e.Kind == ParseKindUnspecified {
			t.Errorf("%q: a *ParseError with no kind", e.Error())
		}
	}
	switch e := err.(type) {
	case interface{ Unwrap() []error }:
		for _, inner := range e.Unwrap() {
			assertKinded(t, inner)
		}
	case interface{ Unwrap() error }:
		assertKinded(t, e.Unwrap())
	}
}

func kindConformanceCases() []dataCase {
	return []dataCase{
		{"KIND-01", func(t *testing.T) { // an environment value outside its enum
			wantKind(t, kdRead[kdInputs](t, []string{"KD_MODE=bogus"}, "", "", ""), channelEnv, InputErrorKindEnumViolation)
		}},
		{"KIND-02", func(t *testing.T) { // an environment value its type can't hold
			wantKind(t, kdRead[kdInputs](t, []string{"KD_COUNT=many"}, "", "", ""), channelEnv, InputErrorKindInvalidValue)
		}},
		{"KIND-03", func(t *testing.T) { // a required environment variable that is unset
			wantKind(t, kdRead[kdRequiredInputs](t, nil, "", "", ""), channelEnv, InputErrorKindMissingRequired)
		}},
		{"KIND-04", func(t *testing.T) { // a configuration file that doesn't parse
			wantKind(t, kdRead[kdInputs](t, nil, "", ":: not [ yaml\n  - ::\n", ""), channelConfig, InputErrorKindMalformedDocument)
		}},
		{"KIND-05", func(t *testing.T) { // a configuration file that doesn't match its schema
			schema := `{"type":"object","properties":{"replicas":{"type":"integer"}}}`
			wantKind(t, kdRead[kdInputs](t, nil, "", "replicas: three\n", schema), channelConfig, InputErrorKindSchemaViolation)
		}},
		{"KIND-06", func(t *testing.T) { // a configuration value its type can't hold
			wantKind(t, kdRead[kdInputs](t, nil, "", "replicas: three\n", ""), channelConfig, InputErrorKindInvalidValue)
		}},
		{"KIND-07", func(t *testing.T) { // a configuration path that is a directory
			dir := t.TempDir()
			meta := InputSettings{ConfigFiles: []ConfigFileDef{{Name: "app", Path: dir, Format: "yaml"}}}
			var in kdInputs
			err := NewInputReader(meta).Read(NewContextFor(kdDef(), nil).WithDir(dir).WithEnviron(nil), &in)
			wantKind(t, err, channelConfig, InputErrorKindFileAccess)
		}},
		{"KIND-08", func(t *testing.T) { // a required stdin with nothing piped, and one that doesn't parse
			wantKind(t, kdRead[kdStdinInputs](t, nil, "", "", ""), channelStdin, InputErrorKindMissingRequired)
			wantKind(t, kdRead[kdStdinInputs](t, nil, "{not json", "", ""), channelStdin, InputErrorKindMalformedDocument)
		}},
		{"KIND-09", func(t *testing.T) { // a flag's fallback value its type can't hold
			wantKind(t, kdRead[kdInputs](t, []string{"KD_PORT=high"}, "", "", ""), channelFlag, InputErrorKindInvalidValue)
		}},
		{"KIND-10", func(t *testing.T) { // a profile the configuration file doesn't define
			path := pfWrite(t, t.TempDir(), "app.yaml", pfFile)
			_, err := pfRead(t, []string{"APP_PROFILE=prdo"}, pfSettings(pfFileAt(path)))
			wantKind(t, err, channelConfig, InputErrorKindUnknownProfile)
		}},
	}
}

func TestConformance_KindMatrix(t *testing.T) {
	wiringModes(t, func(t *testing.T) {
		for _, c := range kindConformanceCases() {
			t.Run(c.id, c.check)
		}
	})
}

// Every *InputError and *ParseError the package builds by hand names its kind: the
// constructors take one, and a literal sets one.
func TestConformance_everyErrorLiteralHasAKind(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			id, ok := lit.Type.(*ast.Ident)
			if !ok || (id.Name != "InputError" && id.Name != "ParseError") {
				return true
			}
			for _, el := range lit.Elts {
				if kv, ok := el.(*ast.KeyValueExpr); ok {
					if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Kind" {
						return true
					}
				}
			}
			t.Errorf("%s: a %s literal with no Kind", fset.Position(lit.Pos()), id.Name)
			return true
		})
	}
}

// Each kind has its own label, and a kind the command line shares has the same one.
func TestInputErrorKind_String(t *testing.T) {
	seen := map[string]InputErrorKind{}
	for k := InputErrorKindUnspecified + 1; k <= InputErrorKindInternal; k++ {
		s := k.String()
		if s == "unspecified" || seen[s] != 0 {
			t.Errorf("kind %d: label %q is unspecified or taken", k, s)
		}
		seen[s] = k
	}
	shared := map[InputErrorKind]ParseKind{
		InputErrorKindInvalidValue:        ParseKindInvalidValue,
		InputErrorKindEnumViolation:       ParseKindEnumViolation,
		InputErrorKindConstraintViolation: ParseKindConstraintViolation,
		InputErrorKindMissingRequired:     ParseKindMissingRequired,
		InputErrorKindInternal:            ParseKindInternal,
	}
	for ik, pk := range shared {
		if ik.String() != pk.String() || inputKindOf(pk) != ik {
			t.Errorf("%v and %v should share a name and map to each other", ik, pk)
		}
	}
	if InputErrorKindUnspecified.String() != "unspecified" || InputErrorKind(99).String() != "unspecified" {
		t.Error("the zero and an unknown kind should read unspecified")
	}
}
