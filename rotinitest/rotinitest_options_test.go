package rotinitest_test

import (
	"encoding/json"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/rotinitest"
)

type record struct {
	ID string `json:"id"`
}

type feedBytes struct {
	Feed struct {
		Flags, Arguments struct{}
		Stdin            *[]byte `stdin:"bytes"`
	}
}

type feedJSONL struct {
	Feed struct {
		Flags, Arguments struct{}
		Stdin            *[]record `stdin:"jsonl"`
	}
}

type feedLines struct {
	Feed struct {
		Flags, Arguments struct{}
		Stdin            *[]string `stdin:"lines"`
	}
}

type feedNUL struct {
	Feed struct {
		Flags, Arguments struct{}
		Stdin            *[]string `stdin:"lines,nul"`
	}
}

type feedStream struct {
	Feed struct {
		Flags, Arguments struct{}
		Stdin            iter.Seq2[string, error] `stdin:"lines,stream"`
	}
}

type feedStreamNUL struct {
	Feed struct {
		Flags, Arguments struct{}
		Stdin            iter.Seq2[string, error] `stdin:"lines,stream,nul"`
	}
}

type feedStreamJSONL struct {
	Feed struct {
		Flags, Arguments struct{}
		Stdin            iter.Seq2[record, error] `stdin:"jsonl,stream"`
	}
}

// feedProgram is a root that echoes its stdin payload, of inputs type T, as JSON: a slurped one
// as it is, and a streamed one as the list of items its iterator yields.
func feedProgram[T any]() *rotini.Program {
	run := func(rtx *rotini.Context) {
		in, ok := read[T](rtx)
		if !ok {
			return
		}
		f := reflect.ValueOf(in).Field(0).FieldByName("Stdin")
		var v any
		switch {
		case f.Kind() == reflect.Func && !f.IsNil():
			items := []any{}
			for item, err := range f.Seq2() {
				if e, _ := reflect.TypeAssert[error](err); e != nil {
					rtx.RecordError(e)
					rtx.HaltWithCode(1)
					return
				}
				items = append(items, item.Interface())
			}
			v = items
		case f.Kind() == reflect.Pointer && !f.IsNil():
			v = f.Elem().Interface()
		}
		b, _ := json.Marshal(v)
		fmt.Fprint(rtx.Stdout, string(b))
	}
	def := rotini.Definition{Name: "feed", Handler: "Feed", Inputs: reflect.TypeFor[T]()}
	return rotini.NewProgramFunc(def, func(string) (rotini.Handler, bool) { return &handler{run: run}, true })
}

func seqOf[V any](items ...V) iter.Seq2[V, error] {
	return func(yield func(V, error) bool) {
		for _, it := range items {
			if !yield(it, nil) {
				return
			}
		}
	}
}

func TestRunStdinFormats(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		run  func(t *testing.T) rotinitest.Result
		want string
	}{
		{"bytes", func(t *testing.T) rotinitest.Result {
			var in feedBytes
			in.Feed.Stdin = &[]byte{0, 1, '\n', 0xff}
			return rotinitest.Run(t, feedProgram[feedBytes](), in)
		}, `"AAEK/w=="`},
		{"jsonl", func(t *testing.T) rotinitest.Result {
			var in feedJSONL
			in.Feed.Stdin = &[]record{{ID: "a"}, {ID: "b\nc"}}
			return rotinitest.Run(t, feedProgram[feedJSONL](), in)
		}, `[{"id":"a"},{"id":"b\nc"}]`},
		{"lines keep an empty last line", func(t *testing.T) rotinitest.Result {
			var in feedLines
			in.Feed.Stdin = &[]string{"a", ""}
			return rotinitest.Run(t, feedProgram[feedLines](), in)
		}, `["a",""]`},
		{"NUL-separated lines", func(t *testing.T) rotinitest.Result {
			var in feedNUL
			in.Feed.Stdin = &[]string{"a b\nc", "", "d"}
			return rotinitest.Run(t, feedProgram[feedNUL](), in)
		}, `["a b\nc","","d"]`},
		{"streamed lines", func(t *testing.T) rotinitest.Result {
			var in feedStream
			in.Feed.Stdin = seqOf("x", "y z")
			return rotinitest.Run(t, feedProgram[feedStream](), in)
		}, `["x","y z"]`},
		{"streamed NUL-separated lines", func(t *testing.T) rotinitest.Result {
			var in feedStreamNUL
			in.Feed.Stdin = seqOf("x\ny", "z")
			return rotinitest.Run(t, feedProgram[feedStreamNUL](), in)
		}, `["x\ny","z"]`},
		{"streamed jsonl", func(t *testing.T) rotinitest.Result {
			var in feedStreamJSONL
			in.Feed.Stdin = seqOf(record{ID: "a"}, record{ID: "b"})
			return rotinitest.Run(t, feedProgram[feedStreamJSONL](), in)
		}, `[{"id":"a"},{"id":"b"}]`},
		{"a nil stream pipes nothing", func(t *testing.T) rotinitest.Result {
			return rotinitest.Run(t, feedProgram[feedStream](), feedStream{})
		}, `[]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := tc.run(t)
			if res.Code != 0 || string(res.Stdout) != tc.want {
				t.Fatalf("exit %d, stdout %s, want %s\n%s", res.Code, res.Stdout, tc.want, res.Stderr)
			}
		})
	}
}

func TestRunStdinRefused(t *testing.T) {
	t.Parallel()
	for name, run := range map[string]func(tb testing.TB){
		"a line holding a newline": func(tb testing.TB) {
			var in feedLines
			in.Feed.Stdin = &[]string{"a\nb"}
			rotinitest.Run(tb, feedProgram[feedLines](), in)
		},
		"a line ending in a carriage return": func(tb testing.TB) {
			var in feedLines
			in.Feed.Stdin = &[]string{"a\r"}
			rotinitest.Run(tb, feedProgram[feedLines](), in)
		},
		"an item holding a NUL": func(tb testing.TB) {
			var in feedNUL
			in.Feed.Stdin = &[]string{"a\x00b"}
			rotinitest.Run(tb, feedProgram[feedNUL](), in)
		},
		"a failing iterator": func(tb testing.TB) {
			var in feedStream
			in.Feed.Stdin = func(yield func(string, error) bool) { yield("", fmt.Errorf("boom")) }
			rotinitest.Run(tb, feedProgram[feedStream](), in)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if msg := failure(t, run); msg == "" {
				t.Error("the run didn't fail the test")
			}
		})
	}
}

type clockInputs struct {
	Clk struct {
		Flags struct {
			Since time.Time `rotini:"since"`
		}
		Arguments struct{}
	}
}

func TestClock(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	def := rotini.Definition{
		Name: "clk", Handler: "Clk", Inputs: reflect.TypeFor[clockInputs](),
		Flags: []rotini.FlagDef{{Name: "since", Identifiers: []string{"--since"}, Type: "time.Time", Default: "2h", Relative: "past"}},
	}
	p := rotini.NewProgramFunc(def, func(string) (rotini.Handler, bool) {
		return &handler{run: func(rtx *rotini.Context) {
			in, ok := read[clockInputs](rtx)
			if !ok {
				return
			}
			fmt.Fprintf(rtx.Stdout, "%s %s", rtx.Now().Format(time.RFC3339), in.Clk.Flags.Since.UTC().Format(time.RFC3339))
		}}, true
	})
	res := rotinitest.Run(t, p, clockInputs{}, rotinitest.Clock(now))
	if want := "2026-10-10T12:00:00Z 2026-10-10T10:00:00Z"; string(res.Stdout) != want {
		t.Errorf("stdout %q, want %q\n%s", res.Stdout, want, res.Stderr)
	}
}

type mcRoot struct {
	Flags struct {
		Verbose bool `rotini:"verbose"`
	}
	Arguments struct{}
}

type mcLsInputs struct {
	Box mcRoot
	Ls  struct {
		Flags struct {
			All bool `rotini:"all"`
		}
		Arguments struct {
			Dir string `rotini:"dir"`
		}
	}
}

func multicallProgram() *rotini.Program {
	def := rotini.Definition{
		Name: "box", Handler: "Box", Multicall: &rotini.MulticallDef{Prefix: "box-"},
		Flags: []rotini.FlagDef{{Name: "verbose", Identifiers: []string{"--verbose"}, Type: "bool"}},
		Commands: []rotini.CommandDef{{
			Name: "ls", Handler: "BoxLs", Aliases: []string{"dir"}, Inputs: reflect.TypeFor[mcLsInputs](),
			Flags:     []rotini.FlagDef{{Name: "all", Identifiers: []string{"-a", "--all"}, Type: "bool"}},
			Arguments: []rotini.ArgDef{{Name: "dir", Type: "string"}},
		}},
	}
	return rotini.NewProgramFunc(def, func(name string) (rotini.Handler, bool) {
		return &handler{run: func(rtx *rotini.Context) {
			if name != "BoxLs" {
				fmt.Fprint(rtx.Stdout, "root")
				return
			}
			in, ok := read[mcLsInputs](rtx)
			if !ok {
				return
			}
			fmt.Fprintf(rtx.Stdout, "ls all=%t dir=%s", in.Ls.Flags.All, in.Ls.Arguments.Dir)
		}}, true
	})
}

func TestArgv0(t *testing.T) {
	t.Parallel()
	var in mcLsInputs
	in.Ls.Flags.All = true
	in.Ls.Arguments.Dir = "ls"
	for _, tc := range []struct {
		argv0 string
		argv  []string
	}{
		{"/usr/bin/box-ls", []string{"--all", "--", "ls"}},
		{"box-dir", []string{"--all", "--", "ls"}},
		{"box", []string{"ls", "--all", "--", "ls"}}, // the root's own name runs the root
		{"ls", []string{"ls", "--all", "--", "ls"}},  // without the prefix, too
	} {
		t.Run(tc.argv0, func(t *testing.T) {
			t.Parallel()
			res := rotinitest.Run(t, multicallProgram(), in, rotinitest.Argv0(tc.argv0))
			if !slices.Equal(res.Argv, tc.argv) {
				t.Errorf("argv = %q, want %q", res.Argv, tc.argv)
			}
			if want := "ls all=true dir=ls"; res.Code != 0 || string(res.Stdout) != want {
				t.Errorf("exit %d, stdout %q, want %q\n%s", res.Code, res.Stdout, want, res.Stderr)
			}
		})
	}
	t.Run("a root flag", func(t *testing.T) {
		t.Parallel()
		var in mcLsInputs
		in.Box.Flags.Verbose = true
		res := rotinitest.Run(t, multicallProgram(), in, rotinitest.Argv0("box-ls"))
		if want := []string{"--verbose"}; !slices.Equal(res.Argv, want) || res.Code != 0 {
			t.Errorf("argv = %q (exit %d), want %q\n%s", res.Argv, res.Code, want, res.Stderr)
		}
	})
}
