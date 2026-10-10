package rotini

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// ── the per-run stdin state ──────────────────────────────────────────────────

type stDocStdin struct {
	Name string `json:"name"`
}
type stDocCmd struct {
	Flags     struct{}
	Arguments struct{}
	Stdin     *stDocStdin `stdin:"json"`
}
type stDocInputs struct{ App stDocCmd }

type stYAMLCmd struct {
	Flags     struct{}
	Arguments struct{}
	Stdin     *stDocStdin `stdin:"yaml"`
}
type stYAMLInputs struct{ App stYAMLCmd }

type stListItem struct {
	Name string `json:"name"`
	N    int    `json:"n,omitempty"`
}
type stListStdin []stListItem

type stListCmd struct {
	Flags     struct{}
	Arguments struct{}
	Stdin     *stListStdin `stdin:"json"`
}
type stListInputs struct{ App stListCmd }

type stListYAMLCmd struct {
	Flags     struct{}
	Arguments struct{}
	Stdin     *stListStdin `stdin:"yaml"`
}
type stListYAMLInputs struct{ App stListYAMLCmd }

const stListSchema = `{"type":"array","items":{"type":"object","required":["name"],"properties":{"name":{"type":"string","minLength":1},"n":{"type":"integer"}}}}`

func stRTX(stdin io.Reader) *Context {
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
	rtx.Stdin = stdin
	return rtx
}

func TestStdinState_slurpIsMemoised(t *testing.T) {
	rtx := stRTX(strings.NewReader("hello\n"))
	first, err := rtx.slurpStdin()
	if err != nil {
		t.Fatal(err)
	}
	second, err := rtx.slurpStdin()
	if err != nil {
		t.Fatal(err)
	}
	if first != "hello\n" || second != first {
		t.Errorf("slurps = %q, %q, want the same raw text twice", first, second)
	}
}

func TestStdinState_readerAfterSlurpReplays(t *testing.T) {
	rtx := stRTX(strings.NewReader("abc"))
	if _, err := rtx.slurpStdin(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rtx.stdinState().reader())
	if err != nil || string(got) != "abc" {
		t.Errorf("reader after slurp = %q, %v, want a replay of abc", got, err)
	}
}

func TestStdinState_slurpAfterStartedStreamFails(t *testing.T) {
	rtx := stRTX(strings.NewReader("line one\nline two\n"))
	buf := make([]byte, 4)
	if _, err := rtx.stdinState().reader().Read(buf); err != nil {
		t.Fatal(err)
	}
	_, err := rtx.slurpStdin()
	if !errors.Is(err, errStdinStreaming) || !errors.Is(err, ErrInternal) {
		t.Errorf("slurp after a started stream = %v, want an internal error", err)
	}
}

func TestStdinState_streamIsShared(t *testing.T) {
	rtx := stRTX(strings.NewReader("abcdef"))
	a, b := rtx.stdinState().reader(), rtx.stdinState().reader()
	p := make([]byte, 3)
	if n, _ := a.Read(p); string(p[:n]) != "abc" {
		t.Fatalf("first read = %q", p[:n])
	}
	rest, _ := io.ReadAll(b)
	if string(rest) != "def" {
		t.Errorf("second reader = %q, want it to continue at the shared position", rest)
	}
}

func TestStdinState_resetStdinStartsAfresh(t *testing.T) {
	rtx := stRTX(strings.NewReader("old"))
	if got, _ := rtx.slurpStdin(); got != "old" {
		t.Fatalf("slurp = %q", got)
	}
	rtx.resetStdin(strings.NewReader("new"))
	if got, _ := rtx.slurpStdin(); got != "new" {
		t.Errorf("slurp after resetStdin = %q, want new", got)
	}
	// Assigning Stdin directly is noticed too.
	rtx.Stdin = strings.NewReader("newer")
	if got, _ := rtx.slurpStdin(); got != "newer" {
		t.Errorf("slurp after assigning Stdin = %q, want newer", got)
	}
}

func TestStdinState_regularFileNeedsNoGoroutine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(path, []byte("from a file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	ctx := t.Context()
	rtx := stRTX(f)
	rtx.bindRun(ctx) // a cancellable run: a pipe would get a pump
	before := runtime.NumGoroutine()
	got, err := rtx.slurpStdin()
	if err != nil || got != "from a file\n" {
		t.Fatalf("slurp = %q, %v", got, err)
	}
	if after := runtime.NumGoroutine(); after != before {
		t.Errorf("goroutines %d → %d, want a regular file read directly", before, after)
	}
}

func TestStdinState_terminalReadsEmpty(t *testing.T) {
	rtx := stRTX(nil)
	if got, err := rtx.slurpStdin(); got != "" || err != nil {
		t.Errorf("nil stdin = %q, %v, want empty", got, err)
	}
}

// A canceled read on a pipe whose writer stays open returns at once with the cause.
func TestStdinState_canceledReadReturnsPromptly(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	ctx, cancel := context.WithCancelCause(context.Background())
	rtx := stRTX(r)
	rtx.bindRun(ctx)
	defer rtx.endStdin()
	cause := ExitCause(130)
	time.AfterFunc(50*time.Millisecond, func() { cancel(cause) })

	done := make(chan error, 1)
	go func() {
		_, err := rtx.slurpStdin()
		done <- err
	}()
	select {
	case err := <-done:
		var ie *InputError
		if !errors.As(err, &ie) || ie.Channel != channelStdin || ie.Msg != "reading stdin was interrupted" {
			t.Fatalf("err = %v, want the stdin interruption", err)
		}
		if !errors.Is(err, cause) {
			t.Errorf("err = %v, want the run's cancellation cause reachable", err)
		}
		if code := rtx.code(); code != 130 {
			t.Errorf("exit code = %d, want the signal's 130", code)
		}
	case <-time.After(time.Second):
		t.Fatal("a canceled read did not return within 1s")
	}
}

// A plain cancellation (no signal code) is recorded as the error it is and sets no code.
func TestStdinState_plainCancelIsNotASignal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	ctx, cancel := context.WithCancel(context.Background())
	rtx := stRTX(r)
	rtx.bindRun(ctx)
	time.AfterFunc(20*time.Millisecond, cancel)
	_, err = rtx.slurpStdin()
	if !errors.Is(err, context.Canceled) || fromRunSignal(ctx, err) {
		t.Errorf("err = %v, want context.Canceled and no signal cause", err)
	}
	if rtx.code() != 0 {
		t.Errorf("exit code = %d, want none for a plain cancellation", rtx.code())
	}
}

// A pumped read through a pipe is byte-identical to a direct read, across chunk boundaries.
func TestStdinState_pumpedReadIsExact(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	payload := strings.Repeat("0123456789abcdef", 20000) // 320 KB, several chunks
	go func() {
		_, _ = io.WriteString(w, payload)
		w.Close()
	}()
	rtx := stRTX(r)
	rtx.bindRun(t.Context())
	defer rtx.endStdin()
	got, err := rtx.slurpStdin()
	if err != nil || got != payload {
		t.Errorf("pumped slurp: %d bytes, %v; want %d bytes exactly", len(got), err, len(payload))
	}
}

// A "-" flag value on a held-open pipe also returns when the run is canceled.
func TestStdinState_flagSentinelCanceled(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	ctx, cancel := context.WithCancelCause(context.Background())
	rtx := stRTX(r)
	rtx.bindRun(ctx)
	time.AfterFunc(20*time.Millisecond, func() { cancel(ExitCause(143)) })
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(rtx.flagStdin())
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Cause(ctx)) {
			t.Errorf("err = %v, want the signal cause", err)
		}
	case <-time.After(time.Second):
		t.Fatal("the sentinel read did not return within 1s")
	}
}

// ── a second Inputs call keeps stdin ────────────────────────────────────────

func TestInputReader_stdinReadOncePerRun(t *testing.T) {
	rtx := stRTX(strings.NewReader("hi\n"))
	for i := range 2 {
		var in tbTextInputs
		if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
			t.Fatal(err)
		}
		if in.App.Stdin == nil || *in.App.Stdin != "hi" {
			t.Errorf("call %d: payload = %v, want hi", i+1, in.App.Stdin)
		}
	}
}

// ── byte-order marks ─────────────────────────────────────────────────────────

func TestInputReader_stdinBOM(t *testing.T) {
	const bom = "\xef\xbb\xbf"
	t.Run("json", func(t *testing.T) {
		var in stDocInputs
		if err := NewInputReader(InputSettings{}).Read(stRTX(strings.NewReader(bom+`{"name":"x"}`)), &in); err != nil {
			t.Fatal(err)
		}
		if in.App.Stdin == nil || in.App.Stdin.Name != "x" {
			t.Errorf("payload = %+v", in.App.Stdin)
		}
	})
	t.Run("yaml", func(t *testing.T) {
		var in stYAMLInputs
		if err := NewInputReader(InputSettings{}).Read(stRTX(strings.NewReader(bom+"name: y\n")), &in); err != nil {
			t.Fatal(err)
		}
		if in.App.Stdin == nil || in.App.Stdin.Name != "y" {
			t.Errorf("payload = %+v", in.App.Stdin)
		}
	})
	t.Run("lines", func(t *testing.T) {
		var in tbLinesInputs
		if err := NewInputReader(InputSettings{}).Read(stRTX(strings.NewReader(bom+"a\nb\n")), &in); err != nil {
			t.Fatal(err)
		}
		if in.App.Stdin == nil || !slices.Equal(*in.App.Stdin, []string{"a", "b"}) {
			t.Errorf("payload = %q", *in.App.Stdin)
		}
	})
	t.Run("text", func(t *testing.T) {
		var in tbTextInputs
		if err := NewInputReader(InputSettings{}).Read(stRTX(strings.NewReader(bom+"a\n")), &in); err != nil {
			t.Fatal(err)
		}
		if in.App.Stdin == nil || *in.App.Stdin != "a" {
			t.Errorf("payload = %q", *in.App.Stdin)
		}
	})
	t.Run("UTF-16 is named", func(t *testing.T) {
		var in stDocInputs
		err := NewInputReader(InputSettings{}).Read(stRTX(strings.NewReader("\xff\xfe{\x00}\x00")), &in)
		if err == nil || err.Error() != "stdin is UTF-16 encoded; pipe UTF-8 instead" || !errors.Is(err, ErrUsage) {
			t.Errorf("err = %v, want the UTF-16 usage error", err)
		}
	})
	t.Run("raw state keeps it", func(t *testing.T) {
		rtx := stRTX(strings.NewReader(bom + "a"))
		if got, _ := rtx.slurpStdin(); got != bom+"a" {
			t.Errorf("slurp = %q, want the raw bytes", got)
		}
	})
	t.Run("flag sentinels", func(t *testing.T) {
		fd := FlagDef{Name: "token", Type: "string", From: []string{"file", "stdin"}}
		path := filepath.Join(t.TempDir(), "tok")
		if err := os.WriteFile(path, []byte(bom+"s3cret\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		fromFile, err := resolveFlagValue(fd, "--token", "@"+path, argvAcq{})
		if err != nil || fromFile != "s3cret" {
			t.Errorf("@file = %q, %v", fromFile, err)
		}
		fromStdin, err := resolveFlagValue(fd, "--token", "-", argvAcq{stdin: strings.NewReader(bom + "s3cret\n")})
		if err != nil || fromStdin != "s3cret" {
			t.Errorf("- = %q, %v", fromStdin, err)
		}
	})
}

func TestConfigFile_BOM(t *testing.T) {
	const bom = "\xef\xbb\xbf"
	dir := t.TempDir()
	path := filepath.Join(dir, "app.json")
	if err := os.WriteFile(path, []byte(bom+`{"api":{"endpoint":"https://e","token":"t"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"", `{"type":"object"}`} {
		var in tbInputs
		meta := InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: path, Schema: schema}}}
		if err := NewInputReader(meta).Read(NewContextFor(tbDef(), nil), &in); err != nil {
			t.Fatalf("schema %q: %v", schema, err)
		}
		if in.App.Config.Endpoint != "https://e" {
			t.Errorf("schema %q: endpoint = %q, want it bound through the BOM", schema, in.App.Config.Endpoint)
		}
	}
}

// ── list documents ───────────────────────────────────────────────────────────

func TestInputReader_stdinArrayDocument(t *testing.T) {
	meta := InputSettings{StdinSchemas: map[string]string{"stListStdin": stListSchema}}
	t.Run("json", func(t *testing.T) {
		var in stListInputs
		if err := NewInputReader(meta).Read(stRTX(strings.NewReader(`[{"name":"a","n":1},{"name":"b"}]`)), &in); err != nil {
			t.Fatal(err)
		}
		if in.App.Stdin == nil || len(*in.App.Stdin) != 2 || (*in.App.Stdin)[0] != (stListItem{"a", 1}) {
			t.Errorf("payload = %+v", in.App.Stdin)
		}
	})
	t.Run("yaml", func(t *testing.T) {
		var in stListYAMLInputs
		if err := NewInputReader(meta).Read(stRTX(strings.NewReader("- name: a\n  n: 2\n- name: b\n")), &in); err != nil {
			t.Fatal(err)
		}
		if in.App.Stdin == nil || len(*in.App.Stdin) != 2 || (*in.App.Stdin)[0] != (stListItem{"a", 2}) {
			t.Errorf("payload = %+v", in.App.Stdin)
		}
	})
	t.Run("schema", func(t *testing.T) {
		var in stListInputs
		err := NewInputReader(meta).Read(stRTX(strings.NewReader(`[{"n":1}]`)), &in)
		var ie *InputError
		if !errors.As(err, &ie) || ie.Channel != channelStdin || !errors.Is(err, ErrUsage) {
			t.Fatalf("err = %v, want a stdin usage error", err)
		}
		if !strings.Contains(err.Error(), "name") {
			t.Errorf("err = %q, want it to name the missing field", err)
		}
	})
	t.Run("CheckInputs", func(t *testing.T) {
		rtx := stRTX(nil)
		rtx.WithInputSettings(meta)
		good := stListInputs{App: stListCmd{Stdin: &stListStdin{{Name: "a"}}}}
		if err := rtx.CheckInputs(good, PresenceOf(good)); err != nil {
			t.Errorf("CheckInputs(valid list) = %v", err)
		}
		bad := stListInputs{App: stListCmd{Stdin: &stListStdin{{N: 1}}}}
		if err := rtx.CheckInputs(bad, PresenceOf(bad)); err == nil || !errors.Is(err, ErrUsage) {
			t.Errorf("CheckInputs(invalid list) = %v, want a usage error", err)
		}
	})
}

// ── decode positions ─────────────────────────────────────────────────────────

func TestInputErrorTexts_stdinPosition(t *testing.T) {
	cases := []struct {
		name string
		in   any
		body string
		want string
	}{
		{"json", &stDocInputs{}, "{\"name\":\n  \"x\",\n", `could not parse stdin as json at line 3, column 1: unexpected end of JSON input`},
		{"yaml", &stYAMLInputs{}, "name: [\n", "could not parse stdin as yaml at line "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := NewInputReader(InputSettings{}).Read(stRTX(strings.NewReader(tc.body)), tc.in)
			if err == nil || !strings.HasPrefix(err.Error(), tc.want) {
				t.Errorf("err = %v, want prefix %q", err, tc.want)
			}
			if !errors.Is(err, ErrUsage) {
				t.Errorf("err = %v, want a usage error", err)
			}
		})
	}
}

func TestInputErrorTexts_configParse(t *testing.T) {
	cases := []struct{ ext, body, want string }{
		{"yaml", "defaults: [\n", ":1:11: "},
		{"json", "{\"a\":\n", ":2:1: "},
		{"json", "{\"a\": 1 \"b\": 2}\n", ":1:9: invalid character '\"' after object key:value pair"},
		{"json", "[1]\n", ": the top level must be a mapping"},
		{"toml", "a = \n", ":1:"},
		{"jsonc", "{\"a\": // c\n", ":"},
		{"env", "A=\"unterminated\n", ":"},
	}
	for _, tc := range cases {
		t.Run(tc.ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "c."+tc.ext)
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			var in tbInputs
			err := NewInputReader(InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: path}}}).
				Read(NewContextFor(tbDef(), nil), &in)
			want := "could not parse configuration file " + path + tc.want
			if err == nil || !strings.HasPrefix(err.Error(), want) {
				t.Errorf("err = %v, want prefix %q", err, want)
			}
			if strings.Contains(err.Error(), "could not open") || strings.Contains(err.Error(), `"app"`) {
				t.Errorf("err = %q, want neither 'could not open' nor the logical name", err)
			}
		})
	}
}

func TestInputErrorTexts_configMissingOverride(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "none.yaml")
	src, _, err := (&InputReader{}).openFileSource(ConfigFile{Name: "app"}, map[string]string{"app": missing}, nil)
	if err == nil || src != nil || err.Error() != "could not open configuration file "+missing+": no such file" {
		t.Errorf("err = %v, want the path and the reason", err)
	}
}

type stCfgInputs struct {
	App struct {
		Flags     struct{}
		Arguments struct{}
		Config    struct {
			Port int `rotini:"port" recon:"api.port"`
		}
	}
}

func TestInputErrorTexts_configValue(t *testing.T) {
	path := writeConfig(t, "api:\n  port: abc\n")
	var in stCfgInputs
	err := NewInputReader(InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: path}}}).
		Read(stRTX(nil), &in)
	want := "config key api.port: expected int (from configuration file " + path + ")"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

// ── env labels and values ────────────────────────────────────────────────────

type stEnvCmd struct {
	Flags     struct{}
	Arguments struct{}
	Env       struct {
		Region string            `rotini:"region" recon:"region,required" env:"APP_REGION,REGION"`
		Port   int               `rotini:"port" recon:"port" env:"APP_PORT"`
		Labels map[string]string `rotini:"labels" recon:"labels" env:"APP_LABELS"`
	}
}
type stEnvInputs struct{ App stEnvCmd }

func stEnvRead(t *testing.T, env ...string) error {
	t.Helper()
	rtx := stRTX(nil)
	rtx.WithEnviron(env)
	var in stEnvInputs
	return NewInputReader(InputSettings{}).Read(rtx, &in)
}

func TestInputErrorTexts_env(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		want string
	}{
		{"required names every spelling", nil, "environment variable APP_REGION (or REGION) is required"},
		{"coercion names the variable set", []string{"REGION=us", "APP_PORT=abc"}, "environment variable APP_PORT: expected int"},
		{"a map entry needs key=value", []string{"APP_REGION=us", "APP_LABELS=a=1,bad"}, `environment variable APP_LABELS expects key=value pairs (got "bad")`},
		{"a map entry needs a key", []string{"APP_REGION=us", "APP_LABELS==v"}, `environment variable APP_LABELS expects key=value pairs (got "=v")`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := stEnvRead(t, tc.env...)
			if err == nil || err.Error() != tc.want {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
			if err != nil && !errors.Is(err, ErrUsage) {
				t.Errorf("err = %v, want a usage error", err)
			}
		})
	}
	if err := stEnvRead(t, "APP_REGION=us", "APP_LABELS=a=1,b=2"); err != nil {
		t.Errorf("well-formed map: %v", err)
	}
}

func TestCheckInputs_envRequiredNamesVariable(t *testing.T) {
	rtx := stRTX(nil)
	err := rtx.CheckInputs(stEnvInputs{}, Presence{})
	want := "environment variable APP_REGION (or REGION) is required"
	if err == nil || err.Error() != want {
		t.Errorf("CheckInputs = %v, want %q", err, want)
	}
}

// ── XDG ──────────────────────────────────────────────────────────────────────

func TestDiscoverDirs_relativeXDGIgnored(t *testing.T) {
	home := t.TempDir()
	view := newOSView([]string{"XDG_CONFIG_HOME=rel", "HOME=" + home, "USERPROFILE=" + home}, "", runtime.GOOS)
	dirs, err := discoverDirs(&DiscoverDef{Strategy: "xdg", App: "app"}, view)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".config", "app"); len(dirs) != 1 || dirs[0] != want {
		t.Errorf("dirs = %v, want [%s]", dirs, want)
	}
}

func TestXDGConfigDir(t *testing.T) {
	home := t.TempDir()
	abs := filepath.Join(home, "xdg")
	cases := []struct {
		name, xdg, app, want string
		bad                  bool
	}{
		{name: "set", xdg: abs, app: "app", want: filepath.Join(abs, "app")},
		{name: "unset", app: "app", want: filepath.Join(home, ".config", "app")},
		{name: "relative", xdg: "rel/dir", app: "app", want: filepath.Join(home, ".config", "app")},
		{name: "empty app", app: "", bad: true},
		{name: "dot", app: ".", bad: true},
		{name: "dotdot", app: "..", bad: true},
		{name: "separator", app: "a/b", bad: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := []string{"HOME=" + home, "USERPROFILE=" + home}
			if tc.xdg != "" {
				env = append(env, "XDG_CONFIG_HOME="+tc.xdg)
			}
			got, err := newOSView(env, "", runtime.GOOS).xdgConfigDir(tc.app)
			if tc.bad {
				if err == nil {
					t.Errorf("xdgConfigDir(%q) = %q, want an error", tc.app, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("xdgConfigDir = %q, %v, want %q", got, err, tc.want)
			}
		})
	}
}
