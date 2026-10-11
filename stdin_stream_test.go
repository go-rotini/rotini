package rotini

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// ── streamed lines ───────────────────────────────────────────────────────────

type ssLinesCmd struct {
	Flags     struct{}
	Arguments struct{}
	Stdin     iter.Seq2[string, error] `stdin:"lines,stream"`
}
type ssLinesInputs struct{ App ssLinesCmd }

type ssLinesReqCmd struct {
	Flags     struct{}
	Arguments struct{}
	Stdin     iter.Seq2[string, error] `stdin:"lines,stream,required"`
}
type ssLinesReqInputs struct{ App ssLinesReqCmd }

type ssNulCmd struct {
	Flags     struct{}
	Arguments struct{}
	Stdin     iter.Seq2[string, error] `stdin:"lines,stream,nul"`
}
type ssNulInputs struct{ App ssNulCmd }

type ssNulSliceCmd struct {
	Flags     struct{}
	Arguments struct{}
	Stdin     *[]string `stdin:"lines,nul"`
}
type ssNulSliceInputs struct{ App ssNulSliceCmd }

// collect ranges over a stream, stopping at the first error.
func collect(t *testing.T, seq iter.Seq2[string, error]) ([]string, error) {
	t.Helper()
	var out []string
	for line, err := range seq {
		if err != nil {
			return out, err
		}
		out = append(out, line)
	}
	return out, nil
}

func ssRead[T any](t *testing.T, stdin io.Reader, meta InputSettings) (T, error) {
	t.Helper()
	var in T
	err := NewInputReader(meta).Read(stRTX(stdin), &in)
	return in, err
}

func TestStdinStream_lines(t *testing.T) {
	for name, tc := range map[string]struct {
		in   string
		want []string
	}{
		"newline ended":    {"a\nb\n", []string{"a", "b"}},
		"no final newline": {"a\nb", []string{"a", "b"}},
		"crlf":             {"a\r\nb\r\n", []string{"a", "b"}},
		"bom on the first": {"\ufeffa\n\ufeffb\n", []string{"a", "\ufeffb"}},
		"blank lines kept": {"a\n\nb\n", []string{"a", "", "b"}},
		"one empty line":   {"\n", []string{""}},
		"spaces kept":      {"  a  \n", []string{"  a  "}},
	} {
		t.Run(name, func(t *testing.T) {
			in, err := ssRead[ssLinesInputs](t, strings.NewReader(tc.in), InputSettings{})
			if err != nil {
				t.Fatal(err)
			}
			got, err := collect(t, in.App.Stdin)
			if err != nil || !slices.Equal(got, tc.want) {
				t.Errorf("lines = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

// A line far longer than the reader's buffer comes through whole.
func TestStdinStream_longLine(t *testing.T) {
	long := strings.Repeat("x", 5<<20)
	in, err := ssRead[ssLinesInputs](t, strings.NewReader(long+"\nend\n"), InputSettings{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := collect(t, in.App.Stdin)
	if err != nil || len(got) != 2 || got[0] != long || got[1] != "end" {
		t.Errorf("got %d lines (first %d bytes), %v", len(got), len(got[0]), err)
	}
}

func TestStdinStream_emptyPipe(t *testing.T) {
	in, err := ssRead[ssLinesInputs](t, strings.NewReader(""), InputSettings{})
	if err != nil || in.App.Stdin == nil {
		t.Fatalf("an empty pipe: Stdin nil=%v, %v; want a set iterator", in.App.Stdin == nil, err)
	}
	if got, err := collect(t, in.App.Stdin); len(got) != 0 || err != nil {
		t.Errorf("an empty pipe yields %q, %v; want nothing", got, err)
	}

	req, err := ssRead[ssLinesReqInputs](t, strings.NewReader(""), InputSettings{})
	if err != nil {
		t.Fatalf("a required stream errors only when ranged over: %v", err)
	}
	_, err = collect(t, req.App.Stdin)
	if err == nil || err.Error() != "required stdin payload is empty; pipe lines" {
		t.Errorf("required empty stream = %v", err)
	}
	if !errors.Is(err, ErrUsage) {
		t.Errorf("required empty stream is not a usage error: %v", err)
	}
}

// Nothing can be piped from a nil reader (or a terminal): the field stays nil, or a required
// stream is an error at once.
func TestStdinStream_nothingPiped(t *testing.T) {
	in, err := ssRead[ssLinesInputs](t, nil, InputSettings{})
	if err != nil || in.App.Stdin != nil {
		t.Errorf("nil stdin: Stdin nil=%v, %v; want nil and no error", in.App.Stdin == nil, err)
	}
	_, err = ssRead[ssLinesReqInputs](t, nil, InputSettings{})
	if err == nil || !strings.Contains(err.Error(), "required stdin payload is empty; pipe lines") {
		t.Errorf("required with nothing piped = %v", err)
	}
}

// One run reads its stdin once: a break keeps the rest, which the next range (or the next
// Inputs call) continues from.
func TestStdinStream_sharedPosition(t *testing.T) {
	rtx := stRTX(strings.NewReader("1\n2\n3\n4\n"))
	r := NewInputReader(InputSettings{})
	var first ssLinesInputs
	if err := r.Read(rtx, &first); err != nil {
		t.Fatal(err)
	}
	for line := range first.App.Stdin {
		if line == "2" {
			break
		}
	}
	var second ssLinesInputs
	if err := r.Read(rtx, &second); err != nil {
		t.Fatal(err)
	}
	got, err := collect(t, second.App.Stdin)
	if err != nil || !slices.Equal(got, []string{"3", "4"}) {
		t.Errorf("after a break at 2, a second Inputs reads %q, %v; want [3 4]", got, err)
	}
	if got, _ := collect(t, first.App.Stdin); len(got) != 0 {
		t.Errorf("a finished stream yields %q again", got)
	}
}

func TestStdinStream_nul(t *testing.T) {
	for name, tc := range map[string]struct {
		in   string
		want []string
	}{
		"trailing nul":    {"a\x00b c\x00", []string{"a", "b c"}},
		"no trailing nul": {"a\x00b", []string{"a", "b"}},
		"bytes kept":      {"a\r\x00 b\n\x00", []string{"a\r", " b\n"}},
		"bom stripped":    {"\ufeffa\x00", []string{"a"}},
	} {
		t.Run(name, func(t *testing.T) {
			in, err := ssRead[ssNulInputs](t, strings.NewReader(tc.in), InputSettings{})
			if err != nil {
				t.Fatal(err)
			}
			got, err := collect(t, in.App.Stdin)
			if err != nil || !slices.Equal(got, tc.want) {
				t.Errorf("stream = %q, %v; want %q", got, err, tc.want)
			}
			whole, err := ssRead[ssNulSliceInputs](t, strings.NewReader(tc.in), InputSettings{})
			if err != nil || whole.App.Stdin == nil || !slices.Equal(*whole.App.Stdin, tc.want) {
				t.Errorf("slice = %v, %v; want %q", whole.App.Stdin, err, tc.want)
			}
		})
	}
}

// A run canceled while the stream waits on a held-open pipe yields the interruption once, with
// the signal's exit code set.
func TestStdinStream_canceled(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if _, err := w.WriteString("one\n"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	rtx := stRTX(r)
	rtx.bindRun(ctx)
	defer rtx.endStdin()
	var in ssLinesInputs
	if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
		t.Fatal(err)
	}
	cause := ExitCause(130)
	done := make(chan []any, 1)
	go func() {
		var got []any
		for line, err := range in.App.Stdin {
			if err != nil {
				got = append(got, err)
				break
			}
			got = append(got, line)
			cancel(cause)
		}
		done <- got
	}()
	select {
	case got := <-done:
		if len(got) != 2 || got[0] != "one" {
			t.Fatalf("got %v, want the line then the interruption", got)
		}
		err, _ := got[1].(error)
		if !errors.Is(err, cause) || !strings.Contains(err.Error(), "reading stdin was interrupted") {
			t.Errorf("err = %v, want the interruption with the run's cause", err)
		}
		if code := rtx.code(); code != 130 {
			t.Errorf("exit code = %d, want 130", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a canceled stream did not end within 2s")
	}
}

// ── JSON Lines ───────────────────────────────────────────────────────────────

type ssRec struct {
	Name string `json:"name"`
	ID   int64  `json:"id,omitempty"`
}

type ssJSONLCmd struct {
	Flags     struct{}
	Arguments struct{}
	Stdin     iter.Seq2[ssRec, error] `stdin:"jsonl,stream"`
}
type ssJSONLInputs struct{ App ssJSONLCmd }

type ssJSONLSliceCmd struct {
	Flags     struct{}
	Arguments struct{}
	Stdin     *[]ssRec `stdin:"jsonl,required"`
}
type ssJSONLSliceInputs struct{ App ssJSONLSliceCmd }

const ssRecSchema = `{"type":"object","required":["name"],"properties":{"name":{"type":"string","minLength":1},"id":{"type":"integer"}}}`

var ssRecMeta = InputSettings{StdinSchemas: map[string]string{"ssRec": ssRecSchema}}

func collectRecs(seq iter.Seq2[ssRec, error]) ([]ssRec, error) {
	var out []ssRec
	for rec, err := range seq {
		if err != nil {
			return out, err
		}
		out = append(out, rec)
	}
	return out, nil
}

func TestStdinStream_jsonl(t *testing.T) {
	in, err := ssRead[ssJSONLInputs](t, strings.NewReader("{\"name\":\"a\"}\n\n  \r\n{\"name\":\"b\",\"id\":9007199254740993}\r\n"), ssRecMeta)
	if err != nil {
		t.Fatal(err)
	}
	got, err := collectRecs(in.App.Stdin)
	want := []ssRec{{Name: "a"}, {Name: "b", ID: 9007199254740993}}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("records = %+v, %v; want %+v", got, err, want)
	}
}

func TestStdinStream_jsonlErrorsNameTheLine(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"schema": {"{\"name\":\"a\"}\n\n{\"name\":\"\"}\n{\"name\":\"c\"}\n", "stdin line 3: "},
		"decode": {"{\"name\":\"a\"}\n{\"name\":\n", "stdin line 2: not a JSON record"},
		"type":   {"{\"name\":\"a\",\"id\":\"x\"}\n", "stdin line 1: "},
	} {
		t.Run(name, func(t *testing.T) {
			in, err := ssRead[ssJSONLInputs](t, strings.NewReader(tc.in), ssRecMeta)
			if err != nil {
				t.Fatal(err)
			}
			_, err = collectRecs(in.App.Stdin)
			if err == nil || !strings.HasPrefix(err.Error(), tc.want) || !errors.Is(err, ErrUsage) {
				t.Errorf("err = %v, want a usage error starting %q", err, tc.want)
			}
			var stopped bool
			for range in.App.Stdin {
				stopped = true
			}
			_ = stopped
		})
	}
}

func TestStdinStream_jsonlSlice(t *testing.T) {
	in, err := ssRead[ssJSONLSliceInputs](t, strings.NewReader("{\"name\":\"a\"}\n{\"name\":\"b\"}\n"), ssRecMeta)
	if err != nil || in.App.Stdin == nil || len(*in.App.Stdin) != 2 || (*in.App.Stdin)[1].Name != "b" {
		t.Fatalf("records = %+v, %v", in.App.Stdin, err)
	}
	_, err = ssRead[ssJSONLSliceInputs](t, strings.NewReader("{\"name\":\"a\"}\n{}\n"), ssRecMeta)
	if err == nil || !strings.HasPrefix(err.Error(), "stdin line 2: ") {
		t.Errorf("a bad record = %v, want it named by line", err)
	}
	_, err = ssRead[ssJSONLSliceInputs](t, strings.NewReader("\n \n"), ssRecMeta)
	if err == nil || err.Error() != "required stdin payload is empty; pipe JSON Lines" {
		t.Errorf("only blank lines, required = %v", err)
	}
}

// ── bytes ────────────────────────────────────────────────────────────────────

type ssBytesCmd struct {
	Flags     struct{}
	Arguments struct{}
	Stdin     *[]byte `stdin:"bytes"`
}
type ssBytesInputs struct{ App ssBytesCmd }

func TestStdin_bytesExact(t *testing.T) {
	for _, payload := range []string{"\x00\xff\n", "\ufeffhello\r\n", "\xff\xfe\x00a"} {
		in, err := ssRead[ssBytesInputs](t, strings.NewReader(payload), InputSettings{})
		if err != nil || in.App.Stdin == nil || string(*in.App.Stdin) != payload {
			t.Errorf("bytes(%q) = %v, %v; want it exactly", payload, in.App.Stdin, err)
		}
	}
}

// ── reading stdin only when no file is given ─────────────────────────────────

type ssCatArgs struct {
	Files []string `rotini:"files"`
}
type ssCatCmd struct {
	Flags struct {
		Help bool `rotini:"help"`
	}
	Arguments ssCatArgs
	Stdin     *string `stdin:"text,required,unless=files"`
}
type ssCatInputs struct{ App ssCatCmd }

var ssCatDef = Definition{
	Name: "app", Handler: "App",
	Flags:     []FlagDef{{Name: "help", Identifiers: []string{"--help"}, Type: "bool", ShortCircuit: true}},
	Arguments: []ArgDef{{Name: "files", Type: "[]string", Variadic: true}},
}

// readWithin reads ssCatInputs for argv with stdin, failing the test if it does not return
// within d (a read of a held-open pipe would wait forever).
func readWithin(t *testing.T, stdin io.Reader, argv []string, d time.Duration) (ssCatInputs, error) {
	t.Helper()
	type result struct {
		in  ssCatInputs
		err error
	}
	done := make(chan result, 1)
	go func() {
		rtx := NewContextFor(ssCatDef, argv)
		rtx.Stdin = stdin
		var in ssCatInputs
		err := NewInputReader(InputSettings{}).Read(rtx, &in)
		done <- result{in, err}
	}()
	select {
	case r := <-done:
		return r.in, r.err
	case <-time.After(d):
		t.Fatalf("reading %v waited on stdin", argv)
	}
	return ssCatInputs{}, nil
}

// heldPipe is a pipe whose writer stays open until the test ends: reading it never ends.
func heldPipe(t *testing.T) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close(); r.Close() })
	return r
}

func TestStdinUnlessArgument(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	in, err := readWithin(t, heldPipe(t), []string{file}, 2*time.Second)
	if err != nil || in.App.Stdin != nil {
		t.Errorf("with a file: Stdin = %v, %v; want nil, stdin unread", in.App.Stdin, err)
	}

	for name, argv := range map[string][]string{"no file": nil, "dash": {"-"}, "file and dash": {file, "-"}} {
		in, err := readWithin(t, strings.NewReader("piped\n"), argv, 2*time.Second)
		if err != nil || in.App.Stdin == nil || *in.App.Stdin != "piped" {
			t.Errorf("%s: Stdin = %v, %v; want the piped text", name, in.App.Stdin, err)
		}
	}

	_, err = readWithin(t, strings.NewReader(""), nil, 2*time.Second)
	if err == nil || err.Error() != "required stdin payload is empty; pipe a text payload or give <files>" {
		t.Errorf("no file and an empty pipe = %v", err)
	}
}

// A short-circuit flag (--help) never waits on stdin: the field stays nil.
func TestStdin_shortCircuitSkipsTheRead(t *testing.T) {
	in, err := readWithin(t, heldPipe(t), []string{"--help"}, 2*time.Second)
	if err != nil || in.App.Stdin != nil {
		t.Errorf("--help: Stdin = %v, %v; want nil and no error", in.App.Stdin, err)
	}
}

// The per-channel stdin layer gates on the file argument too, and counts a stream as set.
func TestStdinInputs_unlessAndStream(t *testing.T) {
	rtx := NewContextFor(ssCatDef, []string{"a.txt"})
	rtx.Stdin = heldPipe(t)
	layer, err := StdinInputsOf[ssCatInputs](rtx)
	if err != nil || len(layer.Set) != 0 {
		t.Errorf("with a file: set %v, %v; want nothing read", layer.Set, err)
	}

	rtx = stRTX(strings.NewReader("a\n"))
	lines, err := StdinInputsOf[ssLinesInputs](rtx)
	if err != nil || len(lines.Set) != 1 {
		t.Errorf("a stream: set %v, %v; want the Stdin field set", lines.Set, err)
	}
}

// StdinInputsOf is rtx.StdinInputs as a function, for a type parameter.
func StdinInputsOf[T any](rtx *Context) (InputLayer[T], error) { return rtx.StdinInputs[T]() }

// CheckInputs needs a required stdin only when no file is given, and counts a stream as given.
func TestCheckInputs_stdinUnlessAndStream(t *testing.T) {
	rtx := NewContextFor(ssCatDef, nil)
	if err := CheckInputsOf(rtx, ssCatInputs{App: ssCatCmd{Arguments: ssCatArgs{Files: []string{"a.txt"}}}}); err != nil {
		t.Errorf("a file given: %v", err)
	}
	err := CheckInputsOf(rtx, ssCatInputs{App: ssCatCmd{Arguments: ssCatArgs{Files: []string{"-"}}}})
	if err == nil || !strings.Contains(err.Error(), "required stdin payload is missing") {
		t.Errorf("only - given: %v, want the missing stdin", err)
	}

	rtx = stRTX(nil)
	seq := func(func(string, error) bool) {}
	if err := CheckInputsOf(rtx, ssLinesReqInputs{App: ssLinesReqCmd{Stdin: seq}}); err != nil {
		t.Errorf("a set stream: %v", err)
	}
}

// CheckInputsOf is rtx.CheckInputs as a function.
func CheckInputsOf[T any](rtx *Context, v T) error { return rtx.CheckInputs(v, PresenceOf(v)) }

// ── memory and throughput ────────────────────────────────────────────────────

// lineSource yields n bytes of 80-byte lines without holding them.
type lineSource struct{ left int }

func (s *lineSource) Read(p []byte) (int, error) {
	if s.left <= 0 {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) && s.left > 0 {
		if s.left%80 == 1 {
			p[n] = '\n'
		} else {
			p[n] = 'x'
		}
		n++
		s.left--
	}
	return n, nil
}

// A streamed lines field reads a large input in constant memory.
func TestStdinStream_constantMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("reads 256 MB")
	}
	const size = 256 << 20
	in, err := ssRead[ssLinesInputs](t, &lineSource{left: size}, InputSettings{})
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var ms runtime.MemStats
	var peak uint64
	n, read := 0, 0
	for line, err := range in.App.Stdin {
		if err != nil {
			t.Fatal(err)
		}
		n++
		read += len(line) + 1
		if read >= 4<<20 {
			read = 0
			runtime.ReadMemStats(&ms)
			peak = max(peak, ms.HeapInuse)
		}
	}
	if n < size/80 {
		t.Fatalf("read %d lines, want about %d", n, size/80)
	}
	if peak > 16<<20 {
		t.Errorf("heap in use peaked at %d MB, want under 16 MB", peak>>20)
	}
}

func BenchmarkStdinJSONL(b *testing.B) {
	var sb strings.Builder
	for i := range 1000 {
		fmt.Fprintf(&sb, "{\"name\":\"task-%d\",\"id\":%d}\n", i, i)
	}
	payload := sb.String()
	b.ReportAllocs()
	b.ResetTimer()
	records := 0
	for b.Loop() {
		var in ssJSONLInputs
		if err := NewInputReader(ssRecMeta).Read(stRTX(strings.NewReader(payload)), &in); err != nil {
			b.Fatal(err)
		}
		for _, err := range in.App.Stdin {
			if err != nil {
				b.Fatal(err)
			}
			records++
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(records), "ns/record")
}
