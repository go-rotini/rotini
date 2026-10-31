package rotini

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

// dirView is a view of the process environment resolving relative paths against dir.
func dirView(dir string) *osView { return (*osView)(nil).withDir(dir) }

// passFS is the operating system's file system as an FS that isn't [OSFS], so a run reads and
// writes through the FS helpers rather than os.
type passFS struct{ osFS }

// throughFS makes every run of the test read and write files through a [Program.WithFS] file
// system, the operating system's.
func throughFS(t *testing.T) {
	t.Helper()
	prev := defaultFS
	defaultFS = passFS{}
	t.Cleanup(func() { defaultFS = prev })
}

// vroot is an absolute directory that exists only in the tests' file systems: /v, or C:\v on
// Windows.
func vroot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(string(filepath.Separator) + "v")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// mapFSOf is a MapFS holding each body at its OS path.
func mapFSOf(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for p, body := range files {
		m[fsNameOf(p)] = &fstest.MapFile{Data: []byte(body), Mode: 0o644}
	}
	return m
}

// memFS is a writable in-memory file system: a MapFS behind a lock.
type memFS struct {
	mu sync.Mutex
	m  fstest.MapFS
}

func newMemFS(files map[string]string) *memFS { return &memFS{m: mapFSOf(files)} }

func (f *memFS) Open(name string) (fs.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m.Open(name)
}

func (f *memFS) Stat(name string) (fs.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m.Stat(name)
}

func (f *memFS) Lstat(name string) (fs.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m.Lstat(name)
}

func (f *memFS) ReadLink(name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m.ReadLink(name)
}

func (f *memFS) ReadDir(name string) ([]fs.DirEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m.ReadDir(name)
}

func (f *memFS) OpenFile(name string, flag int, perm fs.FileMode) (io.WriteCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	_, exists := f.m[name]
	switch {
	case exists && flag&os.O_EXCL != 0:
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrExist}
	case !exists && flag&os.O_CREATE == 0:
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	if dir := path.Dir(name); dir != "." {
		if info, err := f.m.Stat(dir); err != nil || !info.IsDir() {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
		}
	}
	return &memFile{fs: f, name: name, mode: perm}, nil
}

func (f *memFS) MkdirAll(name string, perm fs.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for d := name; d != "."; d = path.Dir(d) {
		if _, err := f.m.Stat(d); err != nil {
			f.m[d] = &fstest.MapFile{Mode: fs.ModeDir | perm}
		}
	}
	return nil
}

func (f *memFS) Rename(oldname, newname string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	mf, ok := f.m[oldname]
	if !ok {
		return &fs.PathError{Op: "rename", Path: oldname, Err: fs.ErrNotExist}
	}
	f.m[newname] = mf
	delete(f.m, oldname)
	return nil
}

func (f *memFS) Remove(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.m[name]; !ok {
		return &fs.PathError{Op: "remove", Path: name, Err: fs.ErrNotExist}
	}
	delete(f.m, name)
	return nil
}

// file is the content and mode at OS path p, and whether it exists.
func (f *memFS) file(p string) (string, fs.FileMode, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	mf, ok := f.m[fsNameOf(p)]
	if !ok {
		return "", 0, false
	}
	return string(mf.Data), mf.Mode, true
}

// names is every name the file system holds, sorted.
func (f *memFS) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for name := range f.m {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// memFile is a file memFS is writing; Close stores it.
type memFile struct {
	fs   *memFS
	name string
	mode fs.FileMode
	buf  bytes.Buffer
}

func (w *memFile) Write(p []byte) (int, error) { return w.buf.Write(p) }

func (w *memFile) Chmod(mode fs.FileMode) error {
	w.mode = mode
	return nil
}

func (w *memFile) Close() error {
	w.fs.mu.Lock()
	defer w.fs.mu.Unlock()
	w.fs.m[w.name] = &fstest.MapFile{Data: bytes.Clone(w.buf.Bytes()), Mode: w.mode}
	return nil
}

// TestFSNames pins the mapping between OS paths and FS names, both ways.
func TestFSNames(t *testing.T) {
	root := vroot(t)
	cases := []string{root, filepath.Join(root, "a"), filepath.Join(root, "a", "b.yaml"), t.TempDir()}
	if runtime.GOOS == "windows" {
		cases = append(cases, `C:\`, `\\host\share\x.yaml`)
	} else {
		cases = append(cases, "/")
	}
	for _, p := range cases {
		name := fsNameOf(p)
		if !fs.ValidPath(name) {
			t.Errorf("fsNameOf(%q) = %q, not a valid FS name", p, name)
			continue
		}
		if back, err := osPathOf("open", name); err != nil || back != p {
			t.Errorf("osPathOf(fsNameOf(%q)) = %q, %v; want the path back", p, back, err)
		}
	}
	if runtime.GOOS != "windows" {
		if got := fsNameOf("/etc/app/config.yaml"); got != "etc/app/config.yaml" {
			t.Errorf("fsNameOf(/etc/app/config.yaml) = %q", got)
		}
		if got := fsNameOf("/"); got != "." {
			t.Errorf("fsNameOf(/) = %q, want .", got)
		}
	}
	if _, err := osPathOf("open", "../x"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("osPathOf(../x) = %v, want fs.ErrInvalid", err)
	}
}

// TestWithFS_theDefault pins that OSFS and nil are the operating system's, read through os
// directly, and that every way of deriving a view keeps the file system.
func TestWithFS_theDefault(t *testing.T) {
	if v := (*osView)(nil).withFS(OSFS()); v.fsys != nil {
		t.Errorf("WithFS(OSFS()) kept %T, want the default", v.fsys)
	}
	m := fstest.MapFS{}
	v := (*osView)(nil).withFS(m)
	for name, derived := range map[string]*osView{
		"withEnviron": v.withEnviron([]string{"A=b"}),
		"withDir":     v.withDir("/x"),
		"withClock":   v.withClock(nil),
		"resolved":    v.withDir("x").resolved(),
		"fileCache":   v.withFileCache(),
		"inputEnv":    v.withInputEnv(nil),
	} {
		if _, ok := derived.fsys.(fstest.MapFS); !ok {
			t.Errorf("%s dropped the file system", name)
		}
	}
	p := NewProgram(Definition{Name: "app", Handler: "App"}, &testHandlers{}).WithFS(m).WithEnviron(nil).WithDir("/w")
	if _, ok := p.view.fsys.(fstest.MapFS); !ok {
		t.Error("Program.WithFS was dropped by a later WithEnviron or WithDir")
	}
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithFS(m)
	if _, ok := rtx.osView().fsys.(fstest.MapFS); !ok {
		t.Error("Context.WithFS did not set the run's file system")
	}
	if (*Context)(nil).WithFS(m) != nil {
		t.Error("WithFS on a nil Context is not nil")
	}
}

// TestWithFS_configFiles pins that config discovery and reading go through the run's file
// system: walk-up, xdg, relative paths, $VAR and ~ in a path, a config_source variable, and a
// parse error naming the file as the operating system's would.
func TestWithFS_configFiles(t *testing.T) {
	root := vroot(t)
	home := filepath.Join(root, "home")
	work := filepath.Join(root, "proj", "sub")
	m := mapFSOf(map[string]string{
		filepath.Join(root, "proj", ".app.yaml"):                  "name: walk-up\n",
		filepath.Join(home, ".config", "acme", "config.yaml"):     "name: xdg-home\n",
		filepath.Join(work, "local.yaml"):                         "name: relative\n",
		filepath.Join(root, "vars", "app.yaml"):                   "name: var\n",
		filepath.Join(home, "tilde.yaml"):                         "name: tilde\n",
		filepath.Join(root, "override.yaml"):                      "name: override\n",
		filepath.Join(work, "bad.yaml"):                           "name: [\n",
		filepath.Join(work, "empty.yaml"):                         "",
		filepath.Join(root, "proj", "sub", "nested", "keep.yaml"): "name: nested\n",
	})
	homeEnv := "HOME=" + home
	if runtime.GOOS == "windows" {
		homeEnv = "USERPROFILE=" + home
	}
	ctx := func(env ...string) *Context {
		return NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithEnviron(env).WithDir(work).WithFS(m)
	}
	tests := []struct {
		name string
		rtx  *Context
		file ConfigFileDef
		want string
	}{
		{"walk-up", ctx(), ConfigFileDef{Name: "p", Discover: &DiscoverDef{Strategy: "walk-up", File: ".app.yaml"}}, "walk-up"},
		{"xdg", ctx(homeEnv), ConfigFileDef{Name: "u", Discover: &DiscoverDef{Strategy: "xdg", File: "config.yaml", App: "acme"}}, "xdg-home"},
		{"a relative path", ctx(), ConfigFileDef{Name: "l", Path: "local.yaml"}, "relative"},
		{"$VAR in a path", ctx("CFG_DIR=" + filepath.Join(root, "vars")), ConfigFileDef{Name: "v", Path: "$CFG_DIR/app.yaml"}, "var"},
		{"~ in a path", ctx(homeEnv), ConfigFileDef{Name: "t", Path: "~/tilde.yaml"}, "tilde"},
		{"config_source variable", ctx("APP_CONFIG=../../override.yaml"), ConfigFileDef{Name: "o", Path: "none.yaml", PathFrom: &PathFromDef{Env: []string{"APP_CONFIG"}}}, "override"},
		{"a missing optional file", ctx(), ConfigFileDef{Name: "m", Path: "missing.yaml"}, ""},
		{"an empty file", ctx(), ConfigFileDef{Name: "e", Path: "empty.yaml"}, ""},
		{"a schema-checked file", ctx(), ConfigFileDef{Name: "s", Path: "nested/keep.yaml", Schema: `{"type":"object","required":["name"]}`}, "nested"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := readCfgName(t, tt.rtx, tt.file); got != tt.want {
				t.Errorf("name = %q, want %q", got, tt.want)
			}
		})
	}

	var in cfgInputs
	err := NewInputReader(InputSettings{ConfigFiles: []ConfigFileDef{{Name: "b", Path: "bad.yaml"}}}).Read(ctx(), &in)
	want := "configuration file " + filepath.Join(work, "bad.yaml") + ":"
	if ie, ok := asError[*InputError](err); !ok || ie.Kind != InputErrorKindMalformedDocument || !strings.Contains(err.Error(), want) {
		t.Errorf("a bad file = %v, want a malformed-document error naming %s with its position", err, want)
	}
	err = NewInputReader(InputSettings{ConfigFiles: []ConfigFileDef{{Name: "o", Path: "none.yaml", PathFrom: &PathFromDef{Env: []string{"APP_CONFIG"}}}}}).Read(ctx("APP_CONFIG=gone.yaml"), &in)
	if ie, ok := asError[*InputError](err); !ok || ie.Kind != InputErrorKindFileAccess || !strings.Contains(err.Error(), "no such file") {
		t.Errorf("a missing named file = %v, want a file-access error saying no such file", err)
	}
	err = NewInputReader(InputSettings{ConfigFiles: []ConfigFileDef{{Name: "s", Path: "local.yaml", Schema: `{"type":"object","required":["region"]}`}}}).Read(ctx(), &in)
	if ie, ok := asError[*InputError](err); !ok || ie.Kind != InputErrorKindSchemaViolation || !strings.Contains(err.Error(), filepath.Join(work, "local.yaml")) {
		t.Errorf("a file its schema rejects = %v, want a schema violation naming the file", err)
	}
	if _, statErr := os.Stat(work); statErr == nil {
		t.Fatalf("%s exists on disk; the test must read only the in-memory file system", work)
	}
}

// TestWithFS_envFiles pins that `.env` files and variable_file values read through the run's
// file system.
func TestWithFS_envFiles(t *testing.T) {
	work := vroot(t)
	m := mapFSOf(map[string]string{
		filepath.Join(work, ".env"):      "APP_REGION=from-dotenv\n",
		filepath.Join(work, "token.txt"): "s3cret\n",
	})
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "region", Identifiers: []string{"--region"}, Type: "string"},
	}}
	type inputs struct {
		App struct {
			Flags struct {
				Region string `rotini:"region" recon:"region" env:"APP_REGION"`
			}
			Arguments struct{}
			Env       struct {
				Token string `rotini:"token" recon:"token" env:"APP_TOKEN" envfile:"APP_TOKEN_FILE"`
			}
		}
	}
	settings := InputSettings{ConfigFiles: []ConfigFileDef{{Name: "dotenv", As: "env", Path: ".env"}}}
	rtx := NewContextFor(def, nil).WithDir(work).WithFS(m).WithEnviron([]string{"APP_TOKEN_FILE=token.txt"})
	var in inputs
	if err := NewInputReader(settings).Read(rtx, &in); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if in.App.Flags.Region != "from-dotenv" || in.App.Env.Token != "s3cret" {
		t.Errorf("bound %+v, want the .env value and the file's contents", in.App)
	}
}

// TestWithFS_argvPaths pins that `@file` values, response files and the path kinds read
// through the run's file system, and that errors name the path as typed.
func TestWithFS_argvPaths(t *testing.T) {
	work := vroot(t)
	m := mapFSOf(map[string]string{
		filepath.Join(work, "token.txt"):          "s3cret\n",
		filepath.Join(work, "args.rsp"):           "--token\n@token.txt\n",
		filepath.Join(work, "data", "keep.txt"):   "x",
		filepath.Join(work, "out", "placeholder"): "",
	})
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "token", Identifiers: []string{"--token"}, Type: "string", From: []string{"file"}},
		{Name: "manifest", Identifiers: []string{"--manifest"}, Type: "existingfile"},
		{Name: "data", Identifiers: []string{"--data"}, Type: "existingdir"},
		{Name: "in", Identifiers: []string{"--in"}, Type: "inputfile"},
		{Name: "out", Identifiers: []string{"--out"}, Type: "outputfile"},
	}}
	type inputs struct {
		App struct {
			Flags struct {
				Token    string `rotini:"token"`
				Manifest string `rotini:"manifest"`
				Data     string `rotini:"data"`
				In       string `rotini:"in"`
				Out      string `rotini:"out"`
			}
		}
	}
	parse := func(argv ...string) (inputs, error) {
		var in inputs
		err := NewParser().Parse(NewContextFor(def, argv).WithDir(work).WithFS(m), &in)
		return in, err
	}
	in, err := parse("--token", "@token.txt", "--manifest", "token.txt", "--data", "data", "--in", "token.txt", "--out", "out/new.txt")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in.App.Flags.Token != "s3cret" || in.App.Flags.Manifest != "token.txt" {
		t.Errorf("bound %+v; want the file's contents and the path as typed", in.App.Flags)
	}
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"--token", "@missing.txt"}, `no such file: "missing.txt"`},
		{[]string{"--token", "@data"}, `"data" is a directory, not a file`},
		{[]string{"--manifest", "missing.txt"}, "missing.txt"},
		{[]string{"--data", "token.txt"}, "token.txt"},
		{[]string{"--in", "missing.txt"}, "missing.txt"},
		{[]string{"--out", "data"}, `"data" is a directory`},
		{[]string{"--out", "nowhere/x.txt"}, "no such directory"},
	} {
		if _, err := parse(tc.argv...); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Parse(%q) = %v, want an error with %q", tc.argv, err, tc.want)
		}
	}

	view := dirView(work).withFS(m)
	words, err := expandResponseFiles([]string{"@args.rsp"}, "@", view, false)
	if err != nil || !slices.Equal(words, []string{"--token", "@token.txt"}) {
		t.Errorf("response file = %q, %v; want its words", words, err)
	}
	if _, err := expandResponseFiles([]string{"@gone.rsp"}, "@", view, false); err == nil {
		t.Error("a missing response file was read")
	}
	if got := expandGlob(view, "dat[a]"); !slices.Equal(got, []string{"data"}) {
		t.Errorf("glob = %q, want the directory the FS holds", got)
	}
	if got := globDirFold(view, work, "*.TXT", nil); !slices.Equal(got, []string{filepath.Join(work, "token.txt")}) {
		t.Errorf("globDirFold = %q, want token.txt matched regardless of case", got)
	}
}

// TestWithFS_OpenInput pins that OpenInput reads the run's file system.
func TestWithFS_OpenInput(t *testing.T) {
	work := vroot(t)
	m := mapFSOf(map[string]string{filepath.Join(work, "in.txt"): "hello", filepath.Join(work, "d", "x"): ""})
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithDir(work).WithFS(m)
	f, err := OpenInput(rtx, "in.txt")
	if err != nil {
		t.Fatalf("OpenInput: %v", err)
	}
	data, _ := io.ReadAll(f)
	_ = f.Close()
	if string(data) != "hello" {
		t.Errorf("read %q", data)
	}
	if _, err := OpenInput(rtx, "missing.txt"); CategoryOf(err) != CategoryUsage || !strings.Contains(err.Error(), `no such file: "missing.txt"`) || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing file = %v, want a usage error matching fs.ErrNotExist", err)
	}
	if _, err := OpenInput(rtx, "d"); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("a directory = %v, want it named one", err)
	}
}

// TestWithFS_writes pins CreateOutput and the config writes through a writable file system.
func TestWithFS_writes(t *testing.T) {
	work := vroot(t)
	mem := newMemFS(map[string]string{filepath.Join(work, "old.txt"): "old"})
	mem.m[fsNameOf(filepath.Join(work, "old.txt"))].Mode = 0o640
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithDir(work).WithFS(mem)

	out, err := CreateOutput(rtx, "new.txt")
	if err != nil {
		t.Fatalf("CreateOutput: %v", err)
	}
	_, _ = io.WriteString(out, "fresh")
	if _, _, ok := mem.file(filepath.Join(work, "new.txt")); ok {
		t.Error("the file is visible before Close")
	}
	if err := out.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if body, mode, _ := mem.file(filepath.Join(work, "new.txt")); body != "fresh" || mode != 0o644 {
		t.Errorf("new.txt = %q %v, want fresh 0644", body, mode)
	}

	if _, err := CreateOutput(rtx, "old.txt"); CategoryOf(err) != CategoryUsage || !errors.Is(err, fs.ErrExist) {
		t.Errorf("an existing file without overwrite = %v, want a usage error matching fs.ErrExist", err)
	}
	out, err = CreateOutput(rtx, "old.txt", OutputOverwrite(true))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(out, "replaced")
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	if body, mode, _ := mem.file(filepath.Join(work, "old.txt")); body != "replaced" || mode != 0o640 {
		t.Errorf("old.txt = %q %v, want replaced, keeping 0640", body, mode)
	}

	out, err = CreateOutput(rtx, "aborted.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(out, "x")
	if err := out.Abort(); err != nil {
		t.Fatal(err)
	}
	for _, name := range mem.names() {
		if strings.Contains(name, "aborted") || strings.HasSuffix(name, ".tmp") {
			t.Errorf("%s left behind", name)
		}
	}
	if _, err := CreateOutput(rtx, "nowhere/x.txt"); CategoryOf(err) != CategoryUsage {
		t.Errorf("a missing directory = %v, want a usage error", err)
	}

	cfg := filepath.Join(work, "conf", "app.yaml")
	rtx = cwContext(work, nil, cwFile(cfg)).WithFS(mem)
	if err := SetConfigValue(rtx, "app", "region", "eu"); err != nil {
		t.Fatalf("SetConfigValue: %v", err)
	}
	if body, mode, _ := mem.file(cfg); body != "region: eu\n" || mode != 0o600 {
		t.Errorf("app.yaml = %q %v, want region: eu, 0600 (its keys include a secret)", body, mode)
	}
	if err := SetConfigValue(rtx, "app", "level", "info"); err != nil {
		t.Fatal(err)
	}
	if err := UnsetConfigValue(rtx, "app", "region"); err != nil {
		t.Fatal(err)
	}
	if body, _, _ := mem.file(cfg); body != "level: info\n" {
		t.Errorf("app.yaml = %q after set and unset", body)
	}
	if p, exists, err := ConfigFilePath(rtx, "app"); err != nil || p != cfg || !exists {
		t.Errorf("ConfigFilePath = %q %v %v", p, exists, err)
	}
	var in struct {
		App struct {
			Config struct {
				Level string `rotini:"level" recon:"level"`
			}
		}
	}
	if err := NewInputReader(pfSettings(cwFile(cfg))).Read(rtx, &in); err != nil || in.App.Config.Level != "info" {
		t.Errorf("read back level = %q, %v", in.App.Config.Level, err)
	}
	if _, err := os.Stat(work); err == nil {
		t.Fatalf("%s exists on disk; the writes must stay in memory", work)
	}
}

// TestWithFS_readOnly pins that a file system that can't be written refuses every write with a
// clear error, while reads, and ConfigFilePath, still work.
func TestWithFS_readOnly(t *testing.T) {
	work := vroot(t)
	cfg := filepath.Join(work, "app.yaml")
	m := mapFSOf(map[string]string{cfg: "region: eu\n"})
	rtx := cwContext(work, nil, cwFile(cfg)).WithFS(m)

	err := SetConfigValue(rtx, "app", "region", "us")
	if ie, ok := asError[*InputError](err); !ok || ie.Kind != InputErrorKindWriteRefused || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("SetConfigValue = %v, want a write-refused error saying the file system is read-only", err)
	}
	if string(m[fsNameOf(cfg)].Data) != "region: eu\n" {
		t.Error("the file changed")
	}
	if p, exists, err := ConfigFilePath(rtx, "app"); err != nil || p != cfg || !exists {
		t.Errorf("ConfigFilePath = %q %v %v, want the file found", p, exists, err)
	}
	_, err = CreateOutput(rtx, "out.txt")
	if !errors.Is(err, errors.ErrUnsupported) || CategoryOf(err) != CategoryNone || !strings.Contains(err.Error(), `could not create output file "out.txt": the program's file system is read-only`) {
		t.Errorf("CreateOutput = %v, want an uncategorised error matching errors.ErrUnsupported", err)
	}
	if out, err := CreateOutput(rtx, "-"); err != nil || out.Close() != nil {
		t.Errorf(`CreateOutput("-") = %v, want stdout`, err)
	}
}

// TestWithFS_symlinks pins that an FS's symbolic links are followed where rotini follows the
// operating system's: a config file read and written through a link, and an output file
// replaced through one.
func TestWithFS_symlinks(t *testing.T) {
	work := vroot(t)
	mem := newMemFS(map[string]string{filepath.Join(work, "real", "app.yaml"): "region: eu\n"})
	mem.m[fsNameOf(filepath.Join(work, "app.yaml"))] = &fstest.MapFile{Data: []byte("real/app.yaml"), Mode: fs.ModeSymlink}
	mem.m[fsNameOf(filepath.Join(work, "dangling"))] = &fstest.MapFile{Data: []byte("gone.txt"), Mode: fs.ModeSymlink}
	link := filepath.Join(work, "app.yaml")
	rtx := cwContext(work, nil, cwFile(link)).WithFS(mem)

	var in struct {
		App struct {
			Config struct {
				Region string `rotini:"region" recon:"region"`
			}
		}
	}
	if err := NewInputReader(pfSettings(cwFile(link))).Read(rtx, &in); err != nil || in.App.Config.Region != "eu" {
		t.Fatalf("read through the link = %q, %v", in.App.Config.Region, err)
	}
	if err := SetConfigValue(rtx, "app", "region", "us"); err != nil {
		t.Fatal(err)
	}
	if body, _, _ := mem.file(filepath.Join(work, "real", "app.yaml")); body != "region: us\n" {
		t.Errorf("the link's target = %q, want it written", body)
	}
	if _, mode, _ := mem.file(link); mode&fs.ModeSymlink == 0 {
		t.Error("the link was replaced")
	}
	if _, err := CreateOutput(rtx, "dangling"); CategoryOf(err) != CategoryUsage || !strings.Contains(err.Error(), "symbolic link to a file that doesn't exist") {
		t.Errorf("a dangling link = %v, want a usage error", err)
	}
}

// TestLayerFS pins reading lower under upper at the mount, listing both, and writing only to
// upper.
func TestLayerFS(t *testing.T) {
	root := vroot(t)
	share := filepath.Join(root, "share", "app")
	upper := newMemFS(map[string]string{
		filepath.Join(share, "override.yaml"): "from: upper\n",
		filepath.Join(root, "elsewhere.txt"):  "upper only",
	})
	lower := fstest.MapFS{
		"override.yaml":   {Data: []byte("from: lower\n")},
		"defaults.yaml":   {Data: []byte("from: lower\n")},
		"sub/nested.yaml": {Data: []byte("nested")},
		"elsewhere.txt":   {Data: []byte("not under the mount")},
	}
	l := LayerFS(upper, share, lower)
	read := func(p string) string {
		t.Helper()
		data, err := fs.ReadFile(l, fsNameOf(p))
		if err != nil {
			return "error: " + err.Error()
		}
		return string(data)
	}
	if got := read(filepath.Join(share, "override.yaml")); got != "from: upper\n" {
		t.Errorf("a file in both = %q, want upper's", got)
	}
	if got := read(filepath.Join(share, "defaults.yaml")); got != "from: lower\n" {
		t.Errorf("a file only in lower = %q", got)
	}
	if got := read(filepath.Join(share, "sub", "nested.yaml")); got != "nested" {
		t.Errorf("a nested lower file = %q", got)
	}
	if got := read(filepath.Join(root, "elsewhere.txt")); got != "upper only" {
		t.Errorf("a name outside the mount = %q, want upper's", got)
	}
	if got := read(filepath.Join(root, "missing.txt")); !strings.Contains(got, "not exist") {
		t.Errorf("a name in neither = %q", got)
	}
	if info, err := fs.Stat(l, fsNameOf(share)); err != nil || !info.IsDir() {
		t.Errorf("the mount = %v, %v; want a directory", info, err)
	}
	entries, err := fs.ReadDir(l, fsNameOf(share))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if err != nil || !slices.Equal(names, []string{"defaults.yaml", "elsewhere.txt", "override.yaml", "sub"}) {
		t.Errorf("ReadDir(mount) = %q, %v", names, err)
	}

	w, ok := l.(WriteFS)
	if !ok {
		t.Fatal("LayerFS over a WriteFS is not a WriteFS")
	}
	rtx := cwContext(root, nil, cwFile(filepath.Join(share, "defaults.yaml"))).WithFS(w)
	if err := SetConfigValue(rtx, "app", "region", "eu"); err != nil {
		t.Fatal(err)
	}
	if body, _, _ := upper.file(filepath.Join(share, "defaults.yaml")); body != "from: lower\nregion: eu\n" {
		t.Errorf("upper's defaults.yaml = %q, want lower's copied up with the edit", body)
	}
	if string(lower["defaults.yaml"].Data) != "from: lower\n" {
		t.Error("lower changed")
	}
	if _, ok := LayerFS(fstest.MapFS{}, share, lower).(WriteFS); ok {
		t.Error("LayerFS over a read-only FS is a WriteFS")
	}
	top, err := filepath.Abs(string(filepath.Separator))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := fs.ReadFile(LayerFS(fstest.MapFS{}, top, lower), fsNameOf(filepath.Join(top, "defaults.yaml"))); err != nil || string(got) != "from: lower\n" {
		t.Errorf("mounted at the root = %q, %v", got, err)
	}
}

// TestOSFS pins that OSFS reads and writes the operating system's files by FS name.
func TestOSFS(t *testing.T) {
	dir := t.TempDir()
	fsys := OSFS()
	name := fsNameOf(filepath.Join(dir, "a.txt"))
	w, err := fsys.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(w, "hi")
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fstest.TestFS(subFS(t, fsys, dir), "a.txt"); err != nil {
		t.Error(err)
	}
	if err := fsys.MkdirAll(fsNameOf(filepath.Join(dir, "x", "y")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fsys.Rename(name, fsNameOf(filepath.Join(dir, "x", "y", "b.txt"))); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "x", "y", "b.txt")); err != nil || string(data) != "hi" {
		t.Errorf("renamed file = %q, %v", data, err)
	}
	if err := fsys.Remove(fsNameOf(filepath.Join(dir, "x", "y", "b.txt"))); err != nil {
		t.Fatal(err)
	}
	if _, err := fsys.Open("../x"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Open(../x) = %v, want fs.ErrInvalid", err)
	}
}

// subFS is fsys below the OS directory dir.
func subFS(t *testing.T, fsys fs.FS, dir string) fs.FS {
	t.Helper()
	sub, err := fs.Sub(fsys, fsNameOf(dir))
	if err != nil {
		t.Fatal(err)
	}
	return sub
}
