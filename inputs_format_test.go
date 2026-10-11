package rotini

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// formatFixture binds the acme deploy command from every channel, in a directory holding a
// main config file and a walk-up project file.
func formatFixture(t *testing.T, argv ...string) InputReport {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"acme.yaml":  "acme:\n  env: from-main\n",
		".acme.yaml": "acme:\n  output: from-project\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rtx := NewContextFor(acmeDef(), argv).WithInputSettings(acmeMeta(dir)).
		WithEnviron([]string{"ACME_REGION=eu", "ACME_HTTP__TIMEOUT=5", "XDG_CONFIG_HOME=" + filepath.Join(dir, "xdg")}).
		WithDir(dir)
	defaults, err := rtx.DefaultInputs[acDeployInputs]()
	must(t, err)
	files, err := rtx.FileInputs[acDeployInputs]()
	must(t, err)
	env, err := rtx.EnvInputs[acDeployInputs]()
	must(t, err)
	args, err := rtx.ArgvInputs[acDeployInputs]()
	must(t, err)
	_, rep := MergeInputsWithReport(defaults, files, env, args)
	return rep
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestInputSource_origin(t *testing.T) {
	t.Parallel()
	rep := formatFixture(t, "--verbose", "deploy", "-e", "prod", "--label", "a=b")
	want := map[FieldPath]string{
		"Acme.Flags.Verbose":  "argv:--verbose",
		"Deploy.Flags.Env":    "argv:-e",
		"Deploy.Flags.Labels": "argv:--label",
		"Deploy.Flags.Output": "config:project#acme.output",
		"Deploy.Env.Region":   "env:ACME_REGION",
		"Deploy.Env.HTTP":     "env:ACME_HTTP__*",
	}
	for path, origin := range want {
		if win, ok := rep.Winner(path); !ok || win.Origin != origin {
			t.Errorf("Winner(%s) = %+v (ok %v), want origin %q", path, win, ok, origin)
		}
	}
	var history []string
	for _, s := range rep.History("Deploy.Flags.Env") {
		history = append(history, s.Origin)
	}
	if got := strings.Join(history, " "); got != "default config:main#acme.env argv:-e" {
		t.Errorf("history of Deploy.Flags.Env = %s", got)
	}
}

func TestInputSource_originArgument(t *testing.T) {
	t.Parallel()
	type getInputs struct {
		Acme   acRootCmd
		Widget acWidgetCmd
		Get    struct {
			Flags     struct{}
			Arguments struct {
				Name string `rotini:"name"`
			}
		}
	}
	rtx := NewContextFor(acmeDef(), []string{"widget", "get", "w1"})
	layer, err := rtx.ArgvInputs[getInputs]()
	must(t, err)
	if got := layer.Set["Get.Arguments.Name"].Origin; got != "argv:<name>" {
		t.Errorf("argument origin = %q, want argv:<name>", got)
	}
}

func TestInputReportFormat(t *testing.T) {
	t.Parallel()
	rep := formatFixture(t, "--verbose", "deploy", "-e", "prod")
	var b strings.Builder
	must(t, rep.Format(&b))
	want := `Acme.Flags.Verbose = "true" from argv:--verbose
Deploy.Env.HTTP = (set) from env:ACME_HTTP__*
Deploy.Env.Region = "eu" from env:ACME_REGION
Deploy.Flags.Env = "prod" from argv:-e; overrides config:main#acme.env "from-main", default "dev"
Deploy.Flags.Output = "from-project" from config:project#acme.output; overrides default "table"
`
	if b.String() != want {
		t.Errorf("Format =\n%s\nwant\n%s", b.String(), want)
	}
}

func TestInputReportFormat_empty(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	if err := (InputReport{}).Format(&b); err != nil || b.Len() != 0 {
		t.Errorf("empty report wrote %q, %v", b.String(), err)
	}
}

// A secret field prints [redacted] even when a hand-built layer supplied it as plain text.
func TestInputReportFormat_redactsHandBuiltSecrets(t *testing.T) {
	t.Parallel()
	type loginInputs struct {
		Acme  acRootCmd
		Login struct {
			Flags struct {
				Token string `rotini:"token"`
			}
			Arguments struct{}
		}
	}
	rtx := NewContextFor(acmeDef(), []string{"login", "--token", "argv-secret"})
	argv, err := rtx.ArgvInputs[loginInputs]()
	must(t, err)
	var vault loginInputs
	vault.Login.Flags.Token = "hunter2"
	custom := InputLayer[loginInputs]{Values: vault, Set: Presence{"Login.Flags.Token": {Layer: "vault", Raw: "hunter2"}}}
	_, rep := MergeInputsWithReport(argv, custom)
	var b strings.Builder
	must(t, rep.Format(&b))
	if got, want := b.String(), "Login.Flags.Token = [redacted] from vault; overrides argv:--token [redacted]\n"; got != want {
		t.Errorf("Format = %q, want %q", got, want)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestInputReportFormat_writeError(t *testing.T) {
	t.Parallel()
	rep := formatFixture(t, "deploy")
	if err := rep.Format(failWriter{}); err == nil {
		t.Error("Format returned nil on a failing writer")
	}
}

// A streamed stdin prints (stream) and is never read.
func TestInputReportFormat_stream(t *testing.T) {
	t.Parallel()
	rtx := stRTX(strings.NewReader("a\nb\n"))
	layer, err := StdinInputsOf[ssLinesInputs](rtx)
	must(t, err)
	in, rep := MergeInputsWithReport(layer)
	var b strings.Builder
	must(t, rep.Format(&b))
	if got := b.String(); got != "App.Stdin = (stream) from stdin\n" {
		t.Errorf("Format = %q", got)
	}
	lines, err := collect(t, in.App.Stdin)
	if err != nil || len(lines) != 2 {
		t.Errorf("after Format the stream yields %q, %v; want both lines unread", lines, err)
	}
}
