package rotini

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// The data rows of the input conformance matrix: stdin read only when no file is given,
// streamed, byte-exact or NUL-separated; @file lists; environment values read from files and
// .env files; and the platform's config directories.

type dataCase struct {
	id    string
	check func(t *testing.T)
}

func dataConformanceCases() []dataCase {
	return []dataCase{
		{"STDIN-08", func(t *testing.T) { // a file given: stdin is not read
			in, err := readWithin(t, heldPipe(t), []string{"a.txt"}, 2*time.Second)
			if err != nil || in.App.Stdin != nil {
				t.Errorf("Stdin = %v, %v; want nil", in.App.Stdin, err)
			}
		}},
		{"STDIN-09", func(t *testing.T) { // "-" reads stdin
			in, err := readWithin(t, strings.NewReader("piped"), []string{"a.txt", "-"}, 2*time.Second)
			if err != nil || in.App.Stdin == nil || *in.App.Stdin != "piped" {
				t.Errorf("Stdin = %v, %v; want the piped text", in.App.Stdin, err)
			}
		}},
		{"STDIN-10", func(t *testing.T) { // a streamed lines field
			in, err := ssRead[ssLinesInputs](t, strings.NewReader("a\r\nb"), InputSettings{})
			if err != nil {
				t.Fatal(err)
			}
			if got, err := collect(t, in.App.Stdin); err != nil || !slices.Equal(got, []string{"a", "b"}) {
				t.Errorf("lines = %q, %v", got, err)
			}
		}},
		{"STDIN-11", func(t *testing.T) { // a JSON Lines record that fails its schema names its line
			in, err := ssRead[ssJSONLInputs](t, strings.NewReader("{\"name\":\"a\"}\n{\"id\":1}\n"), ssRecMeta)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := collectRecs(in.App.Stdin); err == nil || !strings.HasPrefix(err.Error(), "stdin line 2: ") {
				t.Errorf("err = %v", err)
			}
		}},
		{"STDIN-12", func(t *testing.T) { // bytes, exactly as piped
			in, err := ssRead[ssBytesInputs](t, strings.NewReader("\ufeff\x00\n"), InputSettings{})
			if err != nil || in.App.Stdin == nil || string(*in.App.Stdin) != "\ufeff\x00\n" {
				t.Errorf("bytes = %v, %v", in.App.Stdin, err)
			}
		}},
		{"STDIN-13", func(t *testing.T) { // NUL-separated lines
			in, err := ssRead[ssNulSliceInputs](t, strings.NewReader("a b\x00c\x00"), InputSettings{})
			if err != nil || in.App.Stdin == nil || !slices.Equal(*in.App.Stdin, []string{"a b", "c"}) {
				t.Errorf("lines = %v, %v", in.App.Stdin, err)
			}
		}},
		{"STDIN-14", func(t *testing.T) { // a short-circuit flag never reads stdin
			in, err := readWithin(t, heldPipe(t), []string{"--help"}, 2*time.Second)
			if err != nil || in.App.Stdin != nil {
				t.Errorf("Stdin = %v, %v; want nil", in.App.Stdin, err)
			}
		}},
		{"LIST-01", func(t *testing.T) { // @file on a list flag: every line, each split on the separator
			dir := t.TempDir()
			idWrite(t, dir, "tags", "a,b\nc,d\n")
			in, err := idBind(t, dir, "", "--tags", "@tags")
			if err != nil || !slices.Equal(in.App.Flags.Tags, []string{"a", "b", "c", "d"}) {
				t.Errorf("tags = %q, %v", in.App.Flags.Tags, err)
			}
		}},
		{"LIST-02", func(t *testing.T) { // @file on a map flag: one entry per line
			dir := t.TempDir()
			idWrite(t, dir, "labels", "k=v\n\nx=y\n")
			in, err := idBind(t, dir, "", "--labels", "@labels")
			if want := map[string]string{"k": "v", "x": "y"}; err != nil || !reflect.DeepEqual(in.App.Flags.Labels, want) {
				t.Errorf("labels = %v, %v", in.App.Flags.Labels, err)
			}
		}},
		{"LIST-03", func(t *testing.T) { // "-" on a NUL-separated list flag
			in, err := idBind(t, t.TempDir(), "x y\x00z\x00", "--paths", "-")
			if err != nil || !slices.Equal(in.App.Flags.Paths, []string{"x y", "z"}) {
				t.Errorf("paths = %q, %v", in.App.Flags.Paths, err)
			}
		}},
		{"ENV-09", func(t *testing.T) { // variable_file reads the value from the named file
			dir := t.TempDir()
			idWrite(t, dir, "token", "s3cret\n")
			in, err := idSecretBind(t, dir, []string{"APP_TOKEN_FILE=token"})
			if err != nil || in.App.Flags.Token != "s3cret" {
				t.Errorf("token = %q, %v", in.App.Flags.Token, err)
			}
		}},
		{"ENV-10", func(t *testing.T) { // the variable and its file form together
			dir := t.TempDir()
			idWrite(t, dir, "token", "s3cret\n")
			if _, err := idSecretBind(t, dir, []string{"APP_TOKEN=a", "APP_TOKEN_FILE=token"}); err == nil {
				t.Error("both set should be a usage error")
			}
		}},
		{"ENV-11", func(t *testing.T) { // a .env file under the real environment
			dir := t.TempDir()
			idWrite(t, dir, ".env", "APP_ENDPOINT=dotenv\n")
			in, err := idEnvBind(t, dir, []string{})
			if err != nil || in.App.Env.Endpoint != "dotenv" {
				t.Errorf("endpoint = %q, %v", in.App.Env.Endpoint, err)
			}
			in, err = idEnvBind(t, dir, []string{"APP_ENDPOINT=real"})
			if err != nil || in.App.Env.Endpoint != "real" {
				t.Errorf("endpoint = %q, %v; want the real environment's", in.App.Env.Endpoint, err)
			}
		}},
		{"PREC-05", func(t *testing.T) { // argv > environment > .env > config > default
			dir := t.TempDir()
			idWrite(t, dir, "conf.yaml", "region: config\n")
			check := func(env []string, argv []string, want string) {
				t.Helper()
				in, err := idEnvBind(t, dir, env, argv...)
				if err != nil || in.App.Flags.Region != want {
					t.Errorf("env %v argv %v: region = %q, %v; want %q", env, argv, in.App.Flags.Region, err, want)
				}
			}
			check(nil, nil, "config")
			idWrite(t, dir, ".env", "APP_REGION=dotenv\n")
			check([]string{}, nil, "dotenv")
			check([]string{"APP_REGION=env"}, nil, "env")
			check([]string{"APP_REGION=env"}, []string{"--region", "argv"}, "argv")
			if err := os.Remove(filepath.Join(dir, "conf.yaml")); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(dir, ".env")); err != nil {
				t.Fatal(err)
			}
			check([]string{}, nil, "def")
		}},
		{"CFG-09", func(t *testing.T) { // native: the platform's config directory
			home := t.TempDir()
			view := newOSView([]string{"HOME=" + home, "USERPROFILE=" + home, "AppData=" + filepath.Join(home, "AppData"), "XDG_CONFIG_HOME=" + filepath.Join(home, "xdg")}, "", runtime.GOOS)
			dirs, err := discoverDirs(&DiscoverDef{Strategy: "native", App: "demo"}, view)
			base, _ := view.userConfigDir()
			if err != nil || len(dirs) != 1 || dirs[0] != filepath.Join(base, "demo") {
				t.Errorf("dirs = %q, %v; want [%s]", dirs, err, filepath.Join(base, "demo"))
			}
		}},
		{"CFG-10", func(t *testing.T) { // xdg-system under the user's file, key by key
			TestDiscover_nativeOverSystem(t)
		}},
	}
}

// TestConformance_DataMatrix runs the data rows of the input conformance matrix. Their IDs are
// in the canonical list TestConformance_matrixComplete checks.
func TestConformance_DataMatrix(t *testing.T) {
	for _, c := range dataConformanceCases() {
		t.Run(c.id, c.check)
	}
}
