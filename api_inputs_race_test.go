package rotini

import (
	"context"
	"io"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

type raceRootCI struct {
	Flags struct {
		Verbose bool `rotini:"verbose"`
	}
	Arguments struct{}
}

type raceAddInputs struct {
	Race    raceRootCI
	RaceAdd struct {
		Flags struct {
			Count int `rotini:"count"`
		}
		Arguments struct {
			Title string `rotini:"title"`
		}
		Env struct {
			Key string `rotini:"key" recon:"key" env:"RACE_KEY"`
		}
	}
}

func raceDef() Definition {
	return Definition{
		Name: "race", Handler: "Race",
		Flags: []FlagDef{{Name: "verbose", Identifiers: []string{"--verbose"}, Type: "bool"}},
		Commands: []CommandDef{{
			Name: "add", Handler: "RaceAdd", Inputs: reflect.TypeFor[raceAddInputs](),
			Flags:     []FlagDef{{Name: "count", Identifiers: []string{"--count"}, Type: "int", Default: "1"}},
			Arguments: []ArgDef{{Name: "title", Type: "string"}},
		}},
	}
}

type raceHandler struct {
	NoHooks
	run func(ctx context.Context, rtx *Context)
}

func (h *raceHandler) Run(ctx context.Context, rtx *Context) { h.run(ctx, rtx) }

// TestConcurrentRunsAndAccessors runs one configured program from many goroutines, each hook
// reading its inputs and the Context accessors from goroutines of its own, under -race. Each
// run must see only its own argv and per-run dependencies.
func TestConcurrentRunsAndAccessors(t *testing.T) {
	shared := NewDependency[string]("shared")
	perRun := NewDependency[int]("per-run")
	var bad atomic.Int32
	handlers := map[string]func(context.Context, *Context){
		"Race": func(context.Context, *Context) {},
		"RaceAdd": func(_ context.Context, rtx *Context) {
			in, err := rtx.Inputs[raceAddInputs]()
			if err != nil {
				rtx.HaltWith(err)
				return
			}
			n := in.RaceAdd.Flags.Count
			rtx.SetDependency(perRun, n)
			var wg sync.WaitGroup
			for range 4 {
				wg.Go(func() {
					if v, _ := rtx.GetDependency(perRun); v != n {
						bad.Add(1)
					}
					if v := rtx.MustGetDependency(shared); v != "s" {
						bad.Add(1)
					}
					if v, _ := rtx.LookupEnv("RACE_KEY"); v != "k" {
						bad.Add(1)
					}
					again, err := rtx.Inputs[raceAddInputs]()
					if err != nil || again.RaceAdd.Flags.Count != n || again.RaceAdd.Env.Key != "k" {
						bad.Add(1)
					}
					if _, rep, err := rtx.InputsWithReport[raceAddInputs](); err != nil || len(rep.Fields()) == 0 {
						bad.Add(1)
					}
					_ = rtx.CheckInputs(again, PresenceOf(again))
					_, _, _ = rtx.PartialInputs[raceAddInputs]()
					_ = rtx.CommandChain()
					_ = rtx.CommandPath()
					_ = rtx.Command()
					_, _ = rtx.DashIndex()
					_ = rtx.Environ()
					_ = rtx.Dir()
					_ = rtx.Now()
					_ = rtx.Usage()
					_ = rtx.Context()
					_ = Deprecations(rtx)
					rtx.SetDependencyIfAbsent(NewDependency[int]("once"), n)
					rtx.RecordInfo("i")
				})
			}
			wg.Wait()
		},
	}
	p := NewProgramFunc(raceDef(), func(name string) (Handler, bool) {
		run, ok := handlers[name]
		return &raceHandler{run: run}, ok
	}).WithInputSettings(InputSettings{EnvPrefix: "RACE"}).
		WithEnviron([]string{"RACE_KEY=k"}).
		WithDependency(shared, "s").
		WithStdin(apiInputsEOF{}).WithStdout(io.Discard).WithStderr(io.Discard).
		WithReporter(func(context.Context, *Context, Outcome) {})

	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			code, err := p.Run([]string{"add", "--count=" + strconv.Itoa(i+2), "t"})
			if code != 0 || err != nil {
				bad.Add(1)
				t.Errorf("run %d: %d %v", i, code, err)
			}
		})
	}
	wg.Wait()
	if n := bad.Load(); n != 0 {
		t.Fatalf("%d accessor checks saw another run's state", n)
	}
}

type apiInputsEOF struct{}

func (apiInputsEOF) Read([]byte) (int, error) { return 0, io.EOF }
