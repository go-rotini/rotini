// Package main is the acceptance-tier fixture binary for rotini's input
// conformance suite (acceptance_test.go builds and runs it as a subprocess —
// the only honest way to witness real exit codes, auto-detected pipes, the
// no-pipe stdin sentinel, the __complete protocol, and signals). Its handlers
// apply the recommended exit-code convention: usage errors map to
// 1, everything else to 1.
package main

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

type cmdNone struct {
	Flags     struct{}
	Arguments struct{}
}

type getInputs struct {
	Acme   cmdNone
	Widget cmdNone
	Get    struct {
		Flags     struct{}
		Arguments struct {
			Name string `rotini:"name"`
		}
	}
}

type applyInputs struct {
	Acme  cmdNone
	Apply struct {
		Flags struct {
			File string `rotini:"file"`
		}
		Arguments struct{}
	}
}

type ingestPayload struct {
	Kind string `recon:"kind"`
}
type ingestInputs struct {
	Acme   cmdNone
	Ingest struct {
		Flags     struct{}
		Arguments struct{}
		Stdin     *ingestPayload `stdin:"yaml"`
	}
}

func definition() rotini.Definition {
	return rotini.Definition{
		Name: "acme", Handler: "Acme",
		Commands: []rotini.CommandDef{
			{Name: "widget", Handler: "AcmeWidget", Summary: "manage widgets", Commands: []rotini.CommandDef{
				{Name: "get", Handler: "AcmeWidgetGet",
					Arguments: []rotini.ArgDef{{Name: "name", Type: "string", Required: true}}},
			}},
			{Name: "apply", Handler: "AcmeApply", Summary: "apply a manifest", Flags: []rotini.FlagDef{
				{Name: "file", Identifiers: []string{"--file", "-f"}, Summary: "manifest path, or - for stdin", Type: "string", From: []string{"value", "stdin"}},
			}},
			{Name: "ingest", Handler: "AcmeIngest"},
			{Name: "sleep", Handler: "AcmeSleep"},
		},
	}
}

// base embeds the no-op hooks; commands implement Run.
type base struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

type noop struct{ base }

func (noop) Run(context.Context, *rotini.Context) {}

// fail reports err to stderr and exits 1. rotini holds no exit-code constants; a
// CLI picks whatever ints it wants.
func fail(rtx *rotini.Context, err error) {
	fmt.Fprintf(rtx.Stderr, "acme: %v\n", err)
	rtx.HaltWithCode(1)
}

type getHandler struct{ base }

func (getHandler) Run(_ context.Context, rtx *rotini.Context) {
	var in getInputs
	if err := rotini.NewParser().Parse(rtx, &in); err != nil {
		fail(rtx, err)
		return
	}
	fmt.Fprintf(rtx.Stdout, "GET %s\n", in.Get.Arguments.Name)
}

type applyHandler struct{ base }

func (applyHandler) Run(_ context.Context, rtx *rotini.Context) {
	var in applyInputs
	if err := rotini.NewParser().Parse(rtx, &in); err != nil {
		fail(rtx, err)
		return
	}
	fmt.Fprintf(rtx.Stdout, "APPLY %s\n", in.Apply.Flags.File)
}

type ingestHandler struct{ base }

func (ingestHandler) Run(_ context.Context, rtx *rotini.Context) {
	var in ingestInputs
	if err := rotini.NewInputReader(rotini.InputSettings{}).Read(rtx, &in); err != nil {
		fail(rtx, err)
		return
	}
	if in.Ingest.Stdin == nil {
		fail(rtx, rotini.UsageError(fmt.Errorf("no payload piped — ingest reads stdin")))
		return
	}
	fmt.Fprintf(rtx.Stdout, "INGEST kind=%s\n", in.Ingest.Stdin.Kind)
}

type sleepHandler struct{ base }

func (sleepHandler) Run(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stdout, "sleeping")
	<-ctx.Done() // the default signal trap cancels this and exits 130/143
}

type handlers struct{}

func (handlers) Acme() rotini.Handler          { return noop{} }
func (handlers) AcmeWidget() rotini.Handler    { return noop{} }
func (handlers) AcmeWidgetGet() rotini.Handler { return getHandler{} }
func (handlers) AcmeApply() rotini.Handler     { return applyHandler{} }
func (handlers) AcmeIngest() rotini.Handler    { return ingestHandler{} }
func (handlers) AcmeSleep() rotini.Handler     { return sleepHandler{} }

func main() {
	rotini.NewProgram(definition(), handlers{}).Execute()
}
