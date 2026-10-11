---
title: "recipes"
---

# recipes

Short, working answers to common tasks. Each assumes a program set up as in the
[guide](/docs). Rotini's own tests build and run the Go code in each, except blocks titled
"sketch", which call libraries Rotini doesn't depend on.

## Working with generated code

### Removing a command

Delete the command from the spec and run `go generate` twice. The first run disables its handler
file, adding `//go:build ignore` so the program still builds while you move anything you want to
keep. The second run deletes the file. If the command comes back before then, the file returns to
the build as it was, and once deleted, a committed file is still in Git's history.

### A dependency opened only when needed

A dependency that is costly to set up, such as a database or an API client, shouldn't slow down
`--help` or the commands that never use it. Register a function that opens it on first use:
`sync.OnceValues` runs the opener once and hands every later caller the same result.

{{< code title="internal/cmd/lazydemo/store.go" language="golang" open="true" collapsible="false" copy="true" >}}
package lazydemo

import (
	"os"
	"strings"
	"sync"

	"github.com/go-rotini/rotini"
)

// Store is costly to open, so only the commands that use it open it.
type Store struct{ values map[string]string }

// storeDep holds a function that opens the store on its first call and returns the same
// store, or the same error, on every later call.
var storeDep = rotini.NewDependency[func() (*Store, error)]("lazydemo.store")

// registerStore makes the store available without opening it. A test that registered its own
// opener first keeps it.
func registerStore(rtx *rotini.Context) {
	rtx.SetDependencyIfAbsent(storeDep, sync.OnceValues(openStore))
}

func openStore() (*Store, error) {
	data, err := os.ReadFile("store.txt")
	if err != nil {
		return nil, err
	}
	s := &Store{values: map[string]string{}}
	for line := range strings.Lines(string(data)) {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			s.values[k] = v
		}
	}
	return s, nil
}
{{< /code >}}

Call `registerStore(rtx)` first in the root handler's `CascadingPreRun`, which runs before every
command. A command that needs the store calls the function; one that doesn't never opens it:

{{< code title="internal/cmd/lazydemo/lazydemo_get.go" language="golang" open="true" collapsible="false" copy="true" >}}
package lazydemo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*lazydemoGetHandler)(nil)

type lazydemoGetHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*lazydemoGetHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[LazydemoGetInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	store, err := rtx.MustGetDependency(storeDep)()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	fmt.Fprintln(rtx.Stdout, store.values[inputs.LazydemoGet.Arguments.Key])
}
{{< /code >}}

A test registers a double under `storeDep` with `Program.WithDependency`, and
`SetDependencyIfAbsent` leaves it in place.

## Shipping

### All man pages in one document

Some packages want a single man page rather than one per command. The man feature's `ManPages()`
returns every visible command's page, root first, and `man` reads several pages in one stream
as one document. Declare a `man` command and print them all:

{{< code title="internal/cmd/mandemo/mandemo_man.go" language="golang" open="true" collapsible="false" copy="true" >}}
package mandemo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*mandemoManHandler)(nil)

type mandemoManHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*mandemoManHandler) Run(ctx context.Context, rtx *rotini.Context) {
	for _, page := range ManPages() {
		if _, err := fmt.Fprint(rtx.Stdout, page.Content); err != nil {
			rtx.HaltWith(err)
			return
		}
	}
}
{{< /code >}}

{{< code title="terminal" language="sh" open="true" collapsible="false" copy="true" >}}
mandemo man > mandemo.1
man ./mandemo.1
{{< /code >}}

### Shipping with GoReleaser

[GoReleaser](https://goreleaser.com) builds, packages and publishes a Go program from one file.
Generate the completion scripts and man pages as [files ready to package](/docs#files-ready-to-package)
first, so the archives and packages can carry them. This configuration builds reproducible
binaries, ships the files in each archive, and publishes a Homebrew cask, a Scoop manifest, a
winget manifest and a krew manifest for a kubectl plugin, with checksums, SBOMs and a keyless
signature:

{{< code title=".goreleaser.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
version: 2
before:
  hooks:
    - go generate ./...
builds:
  - main: ./cmd/todo
    env: [CGO_ENABLED=0]
    flags: [-trimpath]
    ldflags: [-s -w -X main.version={{ .Version }}]
    mod_timestamp: "{{ .CommitTimestamp }}"
    goos: [linux, darwin, windows]
archives:
  - formats: [tar.gz]
    format_overrides:
      - goos: windows
        formats: [zip]
    files: [share/completions/*, share/man/**/*]
homebrew_casks:
  - repository: { owner: me, name: homebrew-tap }
    binaries: [todo]
    manpages: [share/man/man1/todo.1]
    completions:
      bash: share/completions/todo.bash
      zsh: share/completions/_todo
      fish: share/completions/todo.fish
scoops:
  - repository: { owner: me, name: scoop-bucket }
    homepage: https://github.com/me/todo
    description: a small task CLI
    license: MIT
winget:
  - publisher: Me
    short_description: a small task CLI
    license: MIT
    repository: { owner: me, name: winget-pkgs }
checksum:
  name_template: checksums.txt
sboms:
  - artifacts: archive
signs:
  - cmd: cosign
    signature: "${artifact}.sigstore.json"
    args: ["sign-blob", "--bundle=${signature}", "${artifact}", "--yes"]
    artifacts: checksum
{{< /code >}}

- **Reproducible builds:** `-trimpath`, `mod_timestamp` and `CGO_ENABLED=0` make the same commit
  build the same binary. The man pages' date stays empty unless `SOURCE_DATE_EPOCH` is set when
  you generate, so regenerating them in `before.hooks` changes nothing.
- **Homebrew:** use `homebrew_casks`; the older `brews` section is deprecated. An unsigned macOS
  binary is quarantined when downloaded; GoReleaser's cask docs show a `hooks.post.install`
  that removes the attribute, with the caveats.
- **Linux packages:** the `nfpms` entries in [files ready to package](/docs#files-ready-to-package)
  install the scripts where each shell looks. Debian reads zsh functions from
  `/usr/share/zsh/vendor-completions`; Fedora and Arch read `/usr/share/zsh/site-functions`.
- **A kubectl plugin** adds a `krews` entry (`name`, `repository`, `homepage`, `description`,
  `short_description`). Its manifest has `apiVersion: krew.googlecontainertools.github.com/v1alpha2`
  and `kind: Plugin`; after the first release is accepted into the krew index,
  [krew-release-bot](https://krew.sigs.k8s.io/docs/developer-guide/release/automating-updates/)
  opens the update pull requests.
- **winget** requires `publisher`, `short_description` and `license`.
- **Signing** with cosign keyless writes one `checksums.txt.sigstore.json` bundle. Users check it
  with `cosign verify-blob`, then check each download against `checksums.txt`.

Run `goreleaser check` after editing the file, and `goreleaser release --snapshot --clean` to try a
release without publishing.

### A gh extension or a git subcommand

`gh` runs an extension named `gh-<name>` as `gh <name>`, and git runs any `git-<name>` on `PATH` as
`git <name>`. Name the root after the binary and set `display_name` to what the user types, so
help, man and markdown pages read `gh hello` (see [help that reads like the host](/docs#help-that-reads-like-the-host)).
A variable the host sets is read only when declared:

{{< code title="cmd/gh-hello/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
version: 0.0.0
command:
  name: gh-hello
  display_name: gh hello
  summary: say hello from a gh extension
  env:
    - name: host
      summary: the GitHub host gh is talking to
      schema: { type: string, variable: GH_HOST, default: github.com }
  flags:
    - name: help
      summary: print help
      identifiers: [-h, --help]
      cascading: true
      short_circuit: true
      schema: { type: bool }
  commands:
    - name: greet
      summary: print a greeting
{{< /code >}}

**For gh**, the repository must be named `gh-hello`. `gh` passes every word after the extension's
name through unchanged. `gh extension create --precompiled=go hello` scaffolds the repository,
and [`cli/gh-extension-precompile@v2`](https://github.com/cli/gh-extension-precompile) with
`go_version_file: go.mod` builds the release assets, named `gh-hello-<os>-<arch>` (`.exe` on
Windows), on each `v*` tag. gh also sets `GH_TOKEN`, `GH_REPO` and `GH_PROMPT_DISABLED` among
[others](https://cli.github.com/manual/gh_help_environment); declare the ones you read, and honor
`GH_NO_EXTENSION_UPDATE_NOTIFIER` if you add an update notice.

**For git**, name the root `git-hello` with `display_name: git hello`. `git hello --help` becomes
`git help hello`, which opens the man page `git-hello`: install the root's page from the man
feature, which is already named after the root, as `git-hello.1`.

### A CI template

One GitHub Actions job that checks the spec, the generated code, the build and the contract on
every push:

{{< code title=".github/workflows/ci.yaml (full)" language="yaml" open="true" collapsible="false" copy="true" >}}
name: ci
on: [push, pull_request]
jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0 # rotini diff reads the last release tag
      - uses: actions/setup-go@v7
        with:
          go-version-file: go.mod
      - run: go tool rotini validate ./cmd/todo/.rotini.spec.yaml --config ./cmd/todo/.rotini.conf.yaml
      - run: go tool rotini fmt --check ./cmd/todo/.rotini.spec.yaml ./cmd/todo/.rotini.conf.yaml
      - run: go tool rotini generate --dry-run ./cmd/todo/.rotini.spec.yaml
      - run: go vet ./...
      - run: go test -race ./...
      - uses: golang/govulncheck-action@v1
        with:
          go-version-file: go.mod
          go-package: ./...
      - run: go tool rotini diff "git:$(git describe --tags --abbrev=0)" --spec ./cmd/todo/.rotini.spec.yaml
{{< /code >}}

- `generate --dry-run` exits 2 when the committed code, pages or contract are stale; see
  [the companion CLI](/cli#rotini-generate). Setting the conf's `generate.dry_run_env: CI` makes a
  plain `go generate` check instead of write whenever `CI` is set.
- `rotini diff` needs the contract turned on and committed; see [in CI](/docs#in-ci).
- `fmt --check` formats YAML specs and confs only; drop it for other formats.

## Testing

### Debugging completion

The completion scripts ask the program itself what to offer, through a hidden `__complete`
command that takes the words typed so far, the last one being the word under the cursor. Run it
directly to see exactly what a shell receives, with no shell in the way. `''` stands for an
empty word, as when you press TAB after a space:

{{< code title="$ taskr __complete list --status ''" language="text" open="true" collapsible="false" copy="false" >}}
all
done
open
{{< /code >}}

Each line is a candidate; a candidate with a description has it after a tab. Lines starting with
`:rotini:` are instructions for the script rather than candidates: `:rotini:message` is a line
of guidance to show (see [completion messages](/docs#completion-messages)), and
`:rotini:file` asks the shell to complete file names, here only those ending in `.txt`:

{{< code title="$ taskr __complete add ''" language="text" open="true" collapsible="false" copy="false" >}}
:rotini:message a short title, in quotes
{{< /code >}}

{{< code title="$ taskr __complete list --out ''" language="text" open="true" collapsible="false" copy="false" >}}
:rotini:message --out string: write the list to a file
:rotini:file txt
{{< /code >}}

The lines come in a fixed order: candidates, then one `:rotini:message` line per message, then
a `:rotini:option` line (`nospace`, `keep-order`) when the answer has options, then the kind
line (`:rotini:file`, `:rotini:directory`, `:rotini:none` or another
[completion kind](/docs#what-completion-offers)) when the input declares one. A script skips
`:rotini:` lines it doesn't know and the kind line is always last, so a script and a binary
from different rotini releases work together.

When this output is right but the shell shows something else, the problem is in the shell's
setup: reload the script (`source <(taskr completion bash)`) or start a new shell.

### End-to-end tests with testscript

[testscript](https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript) runs script files
against your real program: each script runs commands and checks their output, exit status and
files in a fresh directory. A test in the `main` package hands the program's own `main` to
testscript, so scripts run it as a command without building it first:

{{< code title="cmd/tsdemo/main_test.go" language="golang" open="true" collapsible="false" copy="true" >}}
package main

import (
	"path/filepath"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

// TestMain lets scripts run this program's main as the command tsdemo.
func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){"tsdemo": main})
}

func TestScripts(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir:                 "testdata/script",
		RequireExplicitExec: true,
		Setup: func(env *testscript.Env) error {
			// Each script gets its own config directory, never the user's.
			env.Setenv("XDG_CONFIG_HOME", filepath.Join(env.WorkDir, ".config"))
			return nil
		},
	})
}
{{< /code >}}

{{< code title="cmd/tsdemo/testdata/script/hello.txtar" language="text" open="true" collapsible="false" copy="true" >}}
# hello greets on stdout and writes nothing to stderr.
exec tsdemo hello
stdout '^hello$'
! stderr .

# A mistyped command fails with a usage error.
! exec tsdemo helo
stderr 'unknown command "helo"'
{{< /code >}}

Add the module with `go get github.com/rogpeppe/go-internal`, then `go test ./cmd/tsdemo` runs
every script. Scripts start from a small environment of their own, so your shell's variables
don't leak in; `Setup` adds what the program needs. `go test ./cmd/tsdemo -testwork` keeps each
script's directory for a look afterwards, and files after `-- name --` lines in a script are
written there before it runs. For typed, in-process tests of one command, see
[testing with typed inputs](/docs#testing-with-typed-inputs).

### Coverage of the built binary

Unit tests measure the code they call. A binary built with `-cover` measures what runs when your
end-to-end tests, or you, run the real program. It writes its counters to the directory
`GOCOVERDIR` names, and `go tool covdata` reads them:

{{< code title="$ coverage of a built binary" language="sh" open="true" collapsible="false" copy="true" >}}
go build -cover -coverpkg=./... -o covdemo ./cmd/covdemo
mkdir -p cover
GOCOVERDIR=cover ./covdemo hello
GOCOVERDIR=cover ./covdemo --help
go tool covdata percent -i=cover
go tool covdata textfmt -i=cover -o cover.out
go tool cover -func=cover.out | tail -1
{{< /code >}}

`-coverpkg=./...` counts every package in your module, not only `main`. Each run adds its own
files to the directory, so the report covers every run, and `go tool covdata merge -i=a,b -o merged`
combines directories from several jobs. `cover.out` is the format `go tool cover -html` reads.

Go writes the counters when the program calls `os.Exit` or returns from `main`. `Execute` ends in
`os.Exit` and recovers a panicking hook by default, so a failed or panicking run is counted too;
with `WithPanicRecover(false)` an unrecovered panic loses that run's counters. A binary run
without `GOCOVERDIR` warns on stderr and writes nothing.

### Fuzzing a CLI

A fuzz test feeds the program command lines Go generates and keeps the ones that find a bug.
Run it in-process with `Program.Run`: a usage error is a correct answer to a bad command line, so
only a panic fails the test. `Run` returns every recorded error and panic joined, so `errors.As`
finds a `*rotini.PanicError` with its stack:

{{< code title="internal/cmd/fuzzdemo/fuzz_test.go" language="golang" open="true" collapsible="false" copy="true" >}}
package fuzzdemo

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/go-rotini/rotini"
)

// FuzzRun runs the program on generated command lines, its words joined by NUL bytes. A usage
// error is a correct answer to a bad command line; only a panic fails the test.
func FuzzRun(f *testing.F) {
	f.Add("add\x00buy milk")
	f.Add("add\x00--priority\x00high\x00x")
	f.Add("--help")
	home := f.TempDir()
	f.Fuzz(func(t *testing.T, joined string) {
		p := NewProgram(Handlers()).
			WithEnviron([]string{"HOME=" + home}).
			WithDir(home).
			WithStdin(strings.NewReader("")).
			WithStdout(io.Discard).
			WithStderr(io.Discard)
		_, err := p.Run(strings.Split(joined, "\x00"))
		var pe *rotini.PanicError
		if errors.As(err, &pe) {
			t.Fatalf("panic on %q: %v\n%s", joined, pe.Value, pe.Stack)
		}
	})
}
{{< /code >}}

`go test` runs the seeds as ordinary tests; `go test -fuzz=FuzzRun -fuzztime=30s
./internal/cmd/fuzzdemo` generates new inputs, and saves any that fails under `testdata/fuzz` so
it runs as a seed from then on. `WithEnviron` and `WithDir` keep your environment and config
files out of every run, and an empty stdin keeps a command from waiting for input. A handler that
writes files or calls the network needs a double for the fuzz test, registered with
`WithDependency`.

## Observability

### Tracing with OpenTelemetry

One span per run, named after the invoked command, with every span a command starts nested
under it. The root handler's `CascadingPreRun`, which runs first in every run, starts the span
and passes its context on with [`rtx.SetContext`](/docs#passing-a-context-on), so `Run` and
every later hook receive it:

{{< code title="internal/cmd/tracedemo/tracedemo.go" language="golang" open="true" collapsible="false" copy="true" >}}
package tracedemo

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-rotini/rotini"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var _ rotini.Handler = (*tracedemoHandler)(nil)

var tracer = otel.Tracer("example.com/tracedemo")

type tracedemoHandler struct {
	rotini.NoPreRun
	rotini.NoPostRun

	// span is the run's span. CascadingPostRun receives the context this handler's
	// CascadingPreRun received, not the one it set, so the span is kept here to end it.
	span trace.Span
}

// CascadingPreRun starts the run's span, named after the invoked command, and hands its
// context to every later hook.
func (h *tracedemoHandler) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
	var names []string
	for _, c := range rtx.CommandChain() {
		names = append(names, c.Name)
	}
	ctx, h.span = tracer.Start(ctx, strings.Join(names, " "))
	rtx.SetContext(ctx)
}

func (h *tracedemoHandler) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
	if rtx.Failed() {
		h.span.SetStatus(codes.Error, "the command failed")
	}
	h.span.End()
}

func (*tracedemoHandler) Run(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stderr, rtx.Help())
	rtx.HaltWithCode(1)
}
{{< /code >}}

The root's `CascadingPostRun` receives the context its `CascadingPreRun` received, not the one
it set, so the span is kept in a handler field to end there. A command starts its own spans
from the context it is given:

{{< code title="internal/cmd/tracedemo/tracedemo_work.go" language="golang" open="true" collapsible="false" copy="true" >}}
package tracedemo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*tracedemoWorkHandler)(nil)

type tracedemoWorkHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*tracedemoWorkHandler) Run(ctx context.Context, rtx *rotini.Context) {
	_, span := tracer.Start(ctx, "load")
	defer span.End()
	fmt.Fprintln(rtx.Stdout, "worked")
}
{{< /code >}}

Set up the tracer provider and its exporter in `main.go` as for any Go program. `Execute` exits
the process, so call `Run` instead, shut the provider down to send the last spans, then exit
with the code `Run` returned.

### -v and -vv as log levels

A counted `-v` flag can choose how much a program logs: warnings by default, more with each
`-v`. Declare it on the root as `type: count`, cascading so every command takes it:

{{< code title="cmd/logdemo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
flags:
  - name: verbose
    summary: log more; repeat for more detail
    identifiers: [-v, --verbose]
    cascading: true
    schema: { type: count }
{{< /code >}}

The root handler's `CascadingPreRun`, which runs before every command, builds a `log/slog`
logger at the level the count asks for and sets it as a dependency for this run. Logs go to
`rtx.Stderr`, so stdout keeps the command's output:

{{< code title="internal/cmd/logdemo/logdemo.go" language="golang" open="true" collapsible="false" copy="true" >}}
package logdemo

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*logdemoHandler)(nil)

// Logger is the run's logger, set by the root's CascadingPreRun before any command runs.
var Logger = rotini.NewDependency[*slog.Logger]("logdemo.logger")

type logdemoHandler struct {
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*logdemoHandler) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[LogdemoInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	flags := inputs.Logdemo.Flags

	var level slog.LevelVar // the zero value is Info
	switch {
	case flags.Verbose >= 2:
		level.Set(slog.LevelDebug)
	case flags.Verbose == 1:
		level.Set(slog.LevelInfo)
	default:
		level.Set(slog.LevelWarn)
	}
	logger := slog.New(slog.NewTextHandler(rtx.Stderr, &slog.HandlerOptions{Level: &level}))
	rtx.SetDependency(Logger, logger)

	if flags.Help {
		fmt.Fprintln(rtx.Stdout, rtx.Help())
		rtx.HaltWithCode(0)
	}
}

func (*logdemoHandler) Run(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stderr, rtx.Help())
	rtx.HaltWithCode(1)
}
{{< /code >}}

A command reads the logger where it needs it:

{{< code title="internal/cmd/logdemo/logdemo_run.go" language="golang" open="true" collapsible="false" copy="true" >}}
package logdemo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*logdemoRunHandler)(nil)

type logdemoRunHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*logdemoRunHandler) Run(ctx context.Context, rtx *rotini.Context) {
	log := rtx.MustGetDependency(Logger)
	log.Debug("loading tasks", "file", "tasks.json")
	log.Info("3 tasks to run")
	log.Warn("the cache is stale")
	fmt.Fprintln(rtx.Stdout, "done")
}
{{< /code >}}

{{< code title="terminal" language="text" open="true" collapsible="false" copy="false" >}}
$ logdemo -vv run
time=2026-10-10T09:00:00.000Z level=DEBUG msg="loading tasks" file=tasks.json
time=2026-10-10T09:00:00.000Z level=INFO msg="3 tasks to run"
time=2026-10-10T09:00:00.000Z level=WARN msg="the cache is stale"
done
{{< /code >}}

For logs a machine reads, add a `--log-format` flag and choose `slog.NewJSONHandler` for `json`.

### Profiling flags

Hidden `--cpuprofile` and `--trace` flags let you profile any command without a separate build.
Declare them on the root, cascading and `hidden: true` so help doesn't list them:

{{< code title="cmd/profdemo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
flags:
  - name: cpuprofile
    summary: write a CPU profile to this file
    identifiers: [--cpuprofile]
    cascading: true
    hidden: true
    schema: { type: string }
  - name: trace
    summary: write an execution trace to this file
    identifiers: [--trace]
    cascading: true
    hidden: true
    schema: { type: string }
{{< /code >}}

The root handler starts the profiler and the tracer in its `CascadingPreRun` and stops them in
its `CascadingPostRun`. The open files are kept in handler fields, which belong to this run:

{{< code title="internal/cmd/profdemo/profdemo.go" language="golang" open="true" collapsible="false" copy="true" >}}
package profdemo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"runtime/trace"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*profdemoHandler)(nil)

type profdemoHandler struct {
	rotini.NoPreRun
	rotini.NoPostRun

	// The files stay open from CascadingPreRun until CascadingPostRun closes them.
	cpu, trace *os.File
}

func (h *profdemoHandler) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[ProfdemoInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	flags := inputs.Profdemo.Flags
	if flags.Help {
		fmt.Fprintln(rtx.Stdout, rtx.Help())
		rtx.HaltWithCode(0)
		return
	}
	if flags.Cpuprofile != "" {
		if h.cpu, err = os.Create(filepath.Join(rtx.Dir(), flags.Cpuprofile)); err != nil {
			rtx.HaltWith(err)
			return
		}
		if err := pprof.StartCPUProfile(h.cpu); err != nil {
			rtx.HaltWith(err)
			return
		}
	}
	if flags.Trace != "" {
		if h.trace, err = os.Create(filepath.Join(rtx.Dir(), flags.Trace)); err != nil {
			rtx.HaltWith(err)
			return
		}
		if err := trace.Start(h.trace); err != nil {
			rtx.HaltWith(err)
		}
	}
}

// CascadingPostRun runs after the command, even when it failed or panicked, so the profile
// and the trace are always complete.
func (h *profdemoHandler) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
	if h.cpu != nil {
		pprof.StopCPUProfile()
		if err := h.cpu.Close(); err != nil {
			rtx.RecordError(err)
		}
	}
	if h.trace != nil {
		trace.Stop()
		if err := h.trace.Close(); err != nil {
			rtx.RecordError(err)
		}
	}
}

func (*profdemoHandler) Run(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stderr, rtx.Help())
	rtx.HaltWithCode(1)
}
{{< /code >}}

{{< code title="terminal" language="sh" open="true" collapsible="false" copy="true" >}}
profdemo work --cpuprofile cpu.prof
go tool pprof -top profdemo cpu.prof
profdemo work --trace trace.out
go tool trace trace.out
{{< /code >}}

Teardown runs after a failed command, a signal or a recovered panic, so the files are complete
then too. `rtx.Exit` skips teardown and leaves them unfinished, as does a panic under
`WithTeardownOnPanic(false)`.

### Crash reports

A panic in a handler is recovered and reaches the reporter as a `*rotini.PanicError`, whose
`Stack` holds the goroutine's stack. A reporter can save it for a bug report. Save it only when
the user asks, and never include the command line or inputs, which may hold secrets. Declare
the variable that turns it on, so help, man pages and the contract list it:

{{< code title="cmd/crashdemo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: crashdemo
  env:
    - name: crash-dir
      summary: a directory to save a crash report in after a crash
      schema: { type: string, variable: CRASHDEMO_CRASH_DIR }
{{< /code >}}

The default reporter can't be extended, so this one prints everything the run recorded itself:

{{< code title="internal/cmd/crashdemo/report.go" language="golang" open="true" collapsible="false" copy="true" >}}
package crashdemo

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/go-rotini/rotini"
)

// Report prints what the run recorded to stderr and exits 1 when it failed. After a panic it
// writes a crash report, but only when the user set CRASHDEMO_CRASH_DIR: the report holds the
// panic, its stack, the version and the platform, never the command line or inputs, which may
// hold secrets.
func Report(ctx context.Context, rtx *rotini.Context, out rotini.Outcome) {
	for _, w := range out.Warnings {
		fmt.Fprintln(rtx.Stderr, "Warning:", w)
	}
	for _, err := range out.Errors {
		fmt.Fprintln(rtx.Stderr, "Error:", err)
	}
	for _, p := range out.Panics {
		fmt.Fprintln(rtx.Stderr, "Error:", p)
	}
	if len(out.Panics) > 0 {
		if dir, ok := rtx.LookupEnv("CRASHDEMO_CRASH_DIR"); ok && dir != "" {
			if path, err := writeCrash(dir, rtx.Version(), out.Panics[0]); err != nil {
				fmt.Fprintln(rtx.Stderr, "could not write a crash report:", err)
			} else {
				fmt.Fprintln(rtx.Stderr, "crash report written to", path)
			}
		} else {
			fmt.Fprintln(rtx.Stderr, "This is a bug. To save a crash report to attach to an issue, set CRASHDEMO_CRASH_DIR to a directory.")
		}
	}
	if out.Failed() && ctx.Err() == nil {
		rtx.Exit(1)
	}
}

func writeCrash(dir, version string, p *rotini.PanicError) (string, error) {
	f, err := os.CreateTemp(dir, "crashdemo-*.txt")
	if err != nil {
		return "", err
	}
	_, err = fmt.Fprintf(f, "panic: %v\nversion: %s\nplatform: %s/%s %s\n\n%s",
		p.Value, version, runtime.GOOS, runtime.GOARCH, runtime.Version(), p.Stack)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return f.Name(), err
}
{{< /code >}}

{{< code title="cmd/crashdemo/main.go" language="golang" open="true" collapsible="false" copy="true" >}}
//go:generate go tool rotini generate ./.rotini.spec.yaml --config ./.rotini.conf.yaml
package main

import (
	cmd "example.com/crashdemo/internal/cmd/crashdemo"
)

var version = "1.0.0"

func main() {
	cmd.NewProgram(cmd.Handlers()).
		WithVersion(version).
		WithReporter(cmd.Report).
		Execute()
}
{{< /code >}}

{{< code title="terminal" language="text" open="true" collapsible="false" copy="false" >}}
$ CRASHDEMO_CRASH_DIR=/tmp crashdemo boom
Error: runtime error: index out of range [3] with length 0
crash report written to /tmp/crashdemo-2290116467.txt
{{< /code >}}

To send the report to a service instead, do it from the same place, with a short timeout, and
still only when the user opted in. For a variable that prints the stack on stderr while you
debug, see [conventions](/docs#conventions).

## Input and output

### Writing to a file and to stdout at once

A command can show its output and keep a copy, as `tee` does. Open the file with
`rotini.CreateOutput`, and write once through `io.MultiWriter` with `rtx.WriteOutputTo`. The
file is written to a temporary name and renamed into place by `Close`, so a run that fails
part-way leaves any earlier file as it was. The flag is `type: outputfile`, and `--force`
carries `role: force`:

{{< code title="internal/cmd/teedemo/teedemo_export.go" language="golang" open="true" collapsible="false" copy="true" >}}
package teedemo

import (
	"context"
	"io"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*teedemoExportHandler)(nil)

type teedemoExportHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*teedemoExportHandler) Run(ctx context.Context, rtx *rotini.Context) {
	in, err := rtx.Inputs[TeedemoExportInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	flags := in.TeedemoExport.Flags
	if err := export(rtx, flags.Output, flags.Force); err != nil {
		rtx.HaltWith(err)
	}
}

func export(rtx *rotini.Context, path string, force bool) error {
	out, err := rotini.CreateOutput(rtx, path, rotini.Overwrite(force))
	if err != nil {
		return err
	}
	defer out.Abort() // discards the file unless Close below commits it
	report := TeedemoExportOutput{Count: 3}
	if err := rtx.WriteOutputTo(io.MultiWriter(rtx.Stdout, out), report, "json", nil); err != nil {
		return err
	}
	return out.Close()
}
{{< /code >}}

{{< code title="terminal" language="sh" open="true" collapsible="false" copy="true" >}}
teedemo export -o report.json
teedemo export -o report.json --force
{{< /code >}}

`io.MultiWriter` stops at the first writer that fails, so a closed stdout also ends the file,
and `Close` is never reached. With `-o -` the output goes to stdout twice.

### Filtering JSON output with jq

A command that writes [structured output](/docs#structured-output) as JSON can be filtered with
[jq](https://jqlang.org), with nothing added to your program:

{{< code title="terminal" language="sh" open="true" collapsible="false" copy="true" >}}
todo list -o json | jq -r '.tasks[] | select(.status == "open") | .title'
{{< /code >}}

jq suits filtering, reshaping and combining output, and anything a script does. A
[`--format` template](/docs#templates) suits a line per item for people, typed in one go, and
needs no other program. Users without jq can install gojq, a jq written in Go:
`go install github.com/itchyny/gojq/cmd/gojq@latest`. Field names are the JSON names `-o json`
shows.

## Config, secrets and versions

### Secrets

A secret typed on the command line shows in `ps`, in `/proc/<pid>/cmdline` and in shell
history. An environment variable is better but not good: child processes inherit it, and it
lands in crash dumps and debug output. Prefer a file or stdin, which a secret flag can insist on
(see [secrets on the command line](/docs#secrets-on-the-command-line)), or a file named by a
variable (see [secrets in files and .env files](/docs#secrets-in-files-and-env-files)). Rotini
redacts `secret: true` values in help, errors, provenance and the contract.

For a source Rotini doesn't read, such as a keychain or a credential helper, build a layer of
your own and merge it with Rotini's, so the value is checked like any other (see
[checking inputs you collected yourself](/docs#checking-inputs-you-collected-yourself)). Here a
helper program named by `SECDEMO_CREDENTIAL_HELPER` supplies `--token` when the user didn't:

{{< code title="internal/cmd/secdemo/secdemo_sync.go" language="golang" open="true" collapsible="false" copy="true" >}}
package secdemo

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*secdemoSyncHandler)(nil)

type secdemoSyncHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*secdemoSyncHandler) Run(ctx context.Context, rtx *rotini.Context) {
	argv, err := rtx.ArgvInputs[SecdemoSyncInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	env, err := rtx.EnvInputs[SecdemoSyncInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	layers := []rotini.InputLayer[SecdemoSyncInputs]{env}
	if helper := env.Values.SecdemoSync.Env.CredentialHelper; helper != "" && argv.Values.SecdemoSync.Flags.Token == "" {
		token, err := fromHelper(ctx, helper)
		if err != nil {
			rtx.HaltWith(err)
			return
		}
		var v SecdemoSyncInputs
		v.SecdemoSync.Flags.Token = token
		layers = append(layers, rotini.InputLayer[SecdemoSyncInputs]{Name: "credential-helper", Values: v, Set: rotini.PresenceOf(v)})
	}
	layers = append(layers, argv) // a --token the user gave wins

	in, report := rotini.MergeInputsWithReport(layers...)
	if err := report.Validate(); err != nil {
		rtx.HaltWith(err)
		return
	}
	src, _ := report.Winner("SecdemoSync.Flags.Token")
	fmt.Fprintf(rtx.Stdout, "token %s from %s (%d bytes)\n", src.Raw, src.Layer, len(in.SecdemoSync.Flags.Token))
}

// fromHelper runs a credential helper, `<helper> get`, and returns the first line it prints.
func fromHelper(ctx context.Context, helper string) (string, error) {
	out, err := exec.CommandContext(ctx, helper, "get").Output()
	if err != nil {
		return "", fmt.Errorf("credential helper %s: %w", helper, err)
	}
	token, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(token), nil
}
{{< /code >}}

`rotini.PresenceOf(v)` marks the field the layer filled. Provenance names the layer,
`credential-helper`, and shows a secret field's value as `[redacted]`.

Other sources fit the same layer:

- **The OS keychain**, with `github.com/zalando/go-keyring`: the macOS Keychain, the Secret
  Service on Linux, the Credential Manager on Windows. Store the token once with `keyring.Set`,
  then read it in place of `fromHelper`:

  {{< code title="keychain (sketch)" language="golang" open="true" collapsible="false" copy="true" >}}
token, err := keyring.Get("secdemo", "default")
if errors.Is(err, keyring.ErrNotFound) {
	// no stored token: leave the layer out, so --token is reported missing
}
{{< /code >}}

- **1Password**: `op read op://vault/secdemo/token | secdemo sync --token -` needs no code. `op
  run -- secdemo sync` resolves `op://` references in the environment, so it suits a token read
  from a variable.
- **SOPS**: `sops exec-env secrets.enc.yaml 'secdemo sync'` decrypts into the environment of
  one command. To decrypt in the program, `decrypt.File(path, "yaml")` from
  `github.com/getsops/sops/v3/decrypt` returns the cleartext to read the token from.
- **Credential helpers** that follow git's protocol take `get` and key-value lines on stdin
  (`git credential fill`, `docker-credential-<name> get`); run them with `os/exec` as
  `fromHelper` does.

### Showing where settings came from

A `config list --show-origin` command shows each setting in effect and the source that won.
`rtx.InputsWithReport` returns a report alongside the inputs: `report.Fields()` lists every field
a source set, and `report.Winner(path)` says which source won it, with the raw value (already
`[redacted]` for a secret) and its origin (`env:ORIGINDEMO_REGION`, `config:user#timeout`,
`argv:--region`, `default`). Declare the list as the command's `output:` so scripts can read it
with `-o json`:

{{< code title="internal/cmd/origindemo/origindemo_config_list.go" language="golang" open="true" collapsible="false" copy="true" >}}
package origindemo

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*origindemoConfigListHandler)(nil)

type origindemoConfigListHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

// listing names the flags that shape the listing rather than settings, so they aren't listed.
var listing = map[string]bool{"Help": true, "ShowOrigin": true, "Output": true}

func (*origindemoConfigListHandler) Run(ctx context.Context, rtx *rotini.Context) {
	in, report, err := rtx.InputsWithReport[OrigindemoConfigListInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	var list OrigindemoConfigListOutput
	for _, path := range report.Fields() {
		key := string(path[strings.LastIndex(string(path), ".")+1:])
		src, ok := report.Winner(path)
		if !ok || listing[key] {
			continue
		}
		origin := src.Origin
		if origin == "" {
			origin = src.Layer // a layer you built yourself names only itself
		}
		list = append(list, Setting{Key: key, Value: src.Raw, Source: origin})
	}
	flags := in.OrigindemoConfigList.Flags
	rtx.HaltWith(rtx.WriteOutput(list, flags.Output, func(w io.Writer, _ string, v OrigindemoConfigListOutput) error {
		for _, s := range v {
			line := fmt.Sprintf("%s = %s", s.Key, s.Value)
			if flags.ShowOrigin {
				line += "\t(" + s.Source + ")"
			}
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
		}
		return nil
	}))
}
{{< /code >}}

{{< code title="$ ORIGINDEMO_REGION=us-2 origindemo config list --show-origin" language="text" open="true" collapsible="false" copy="false" >}}
Region = us-2	(env:ORIGINDEMO_REGION)
Timeout = 5s	(config:user#timeout)
{{< /code >}}

For one line per field with everything each value overrode, `report.Format(w)` is enough; see
[explaining where values came from](/docs#explaining-where-values-came-from).

### A --profile flag with completion

A configuration file with [profiles](/docs#profiles) names them under one key, so the selector
flag can offer them. `rotini.ConfigProfiles` reads the file the running command would read,
through its `config_source` path or `discover` search, and returns the names sorted. Implement
`CompleteFlagValue` on the handler of the command that declares the flag:

{{< code title="internal/cmd/app/app_complete.go" language="golang" open="true" collapsible="false" copy="true" >}}
package app

import "github.com/go-rotini/rotini"

// CompleteFlagValue offers the profiles the configuration file defines for --profile.
func (*appHandler) CompleteFlagValue(rtx *rotini.Context, flag, _ string) []string {
	if flag != "profile" {
		return nil
	}
	names, err := rotini.ConfigProfiles(rtx, "app")
	if err != nil {
		return nil
	}
	return names
}
{{< /code >}}

A file that doesn't exist yet offers nothing, and a file that can't be read offers nothing
rather than an error, since completion runs on every keystroke.

### Config set, unset and path commands

[Writing config values](/docs#writing-config-values) gives your program `git config`-style
commands that edit a declared config file and keep its comments. Declare the commands:

{{< code title="cmd/cfgctl/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: cfgctl
  config_files:
    - name: user
      discover: { strategy: xdg, file: config.yaml, app: cfgctl }
  config:
    - name: region
      schema: { type: string }
    - name: level
      schema: { type: string, enum: [debug, info, warn] }
  commands:
    - name: config
      summary: read and write the configuration file
      commands:
        - name: set
          summary: set a configuration value
          arguments:
            - name: key
              schema: { type: string, required: true }
            - name: value
              schema: { type: string, required: true }
        - name: unset
          summary: remove a configuration value
          arguments:
            - name: key
              schema: { type: string, required: true }
        - name: path
          summary: print the configuration file's path
{{< /code >}}

`set` reads only its arguments, so a broken config file doesn't stop it from running, and offers
the file's keys from the generated key table:

{{< code title="internal/cmd/cfgctl/cfgctl_config_set.go" language="golang" open="true" collapsible="false" copy="true" >}}
package cfgctl

import (
	"context"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*cfgctlConfigSetHandler)(nil)

type cfgctlConfigSetHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*cfgctlConfigSetHandler) Run(ctx context.Context, rtx *rotini.Context) {
	in, err := rtx.ArgvInputs[CfgctlConfigSetInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	args := in.Values.CfgctlConfigSet.Arguments
	if err := rotini.SetConfigValue(rtx, "user", args.Key, args.Value); err != nil {
		rtx.HaltWith(err)
	}
}

// CompleteArgValue offers the keys the configuration file may hold.
func (*cfgctlConfigSetHandler) CompleteArgValue(_ *rotini.Context, arg, _ string) []string {
	if arg != "key" {
		return nil
	}
	var keys []string
	for _, f := range InputSettings.ConfigFiles {
		if f.Name == "user" {
			for _, k := range f.Keys {
				keys = append(keys, k.Key)
			}
		}
	}
	return keys
}
{{< /code >}}

`unset` calls `rotini.UnsetConfigValue(rtx, "user", key)` the same way, and `path` prints what
`rotini.ConfigFilePath(rtx, "user")` returns. A rejected value is a usage error and the file is
left as it was:

```text
$ cfgctl config set level loud
Error: invalid value "loud" for config key level (one of: debug, info, warn)
```

### Reloading config on SIGHUP

Rotini traps SIGINT and SIGTERM, which cancel the run's context; SIGHUP is left to you. A
long-running command can take it as "reread the configuration": calling `rtx.Inputs` again
reads every source afresh. Keep the old settings when the new ones don't validate, so a bad edit
doesn't take the server down:

{{< code title="internal/cmd/reloaddemo/reloaddemo_serve.go" language="golang" open="true" collapsible="false" copy="true" >}}
package reloaddemo

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*reloaddemoServeHandler)(nil)

type reloaddemoServeHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*reloaddemoServeHandler) Run(ctx context.Context, rtx *rotini.Context) {
	in, err := rtx.Inputs[ReloaddemoServeInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	cfg := in.ReloaddemoServe.Config

	// Rotini traps SIGINT and SIGTERM, which cancel ctx. SIGHUP is the program's own.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)

	fmt.Fprintln(rtx.Stdout, "serving:", cfg.Greeting)
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(rtx.Stdout, "stopped")
			return
		case <-hup:
			next, err := rtx.Inputs[ReloaddemoServeInputs]()
			if err != nil {
				// A bad edit keeps the settings in use; the server stays up.
				fmt.Fprintln(rtx.Stderr, "reload failed, keeping the old configuration:", err)
				continue
			}
			cfg = next.ReloaddemoServe.Config
			fmt.Fprintln(rtx.Stdout, "reloaded:", cfg.Greeting)
		}
	}
}
{{< /code >}}

{{< code title="terminal" language="sh" open="true" collapsible="false" copy="true" >}}
reloaddemo serve &
kill -HUP $!     # reloaded: …
kill -TERM $!    # stopped; exits 143
{{< /code >}}

Without the `signal.Notify`, SIGHUP ends the process with 129 and no teardown. To reload when
the file changes instead, `NewWatcher` from `github.com/go-rotini/fs` reports changes to a path,
saves by rename included. SIGHUP doesn't exist on Windows.

### The version from the build

A binary built with `go install` or `go build` in a Git checkout already knows its version: the
tag, a pseudo-version after later commits, and `+dirty` with uncommitted changes. The guide's
[versions](/docs#versions) section shows the `main.go` that uses it, and when to stamp the
version with `-ldflags` instead.

## Patterns Rotini leaves to you

### Prompting for missing inputs

Rotini never prompts. To ask for what the command line left out, read the given values without
checking them, ask for the missing ones, then check the result against the spec with
`rtx.CheckInputs`, which applies every rule `rtx.Inputs` would. Ask only when someone can answer:
when stdin is a terminal and the user hasn't passed a declared `--no-input`. Otherwise the
missing input is the usual usage error, so scripts and CI never hang:

{{< code title="internal/cmd/promptdemo/promptdemo_greet.go" language="golang" open="true" collapsible="false" copy="true" >}}
package promptdemo

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*promptdemoGreetHandler)(nil)

type promptdemoGreetHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*promptdemoGreetHandler) Run(ctx context.Context, rtx *rotini.Context) {
	// Read what was given, without checking it: a missing input is not an error yet.
	argv, err := rtx.ArgvInputs[PromptdemoGreetInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	env, err := rtx.EnvInputs[PromptdemoGreetInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	in := rotini.MergeInputs(env, argv)

	// Ask only when someone can answer: stdin is a terminal and --no-input isn't set.
	flags := &in.PromptdemoGreet.Flags
	if flags.Name == "" && !in.Promptdemo.Flags.NoInput && rotini.IsTerminal(rtx.Stdin) {
		flags.Name = ask(rtx, "Name: ")
	}

	// Check the result against the spec, as if every value had been typed.
	if err := rtx.CheckInputs(in, rotini.PresenceOf(in)); err != nil {
		rtx.HaltWith(err)
		return
	}
	fmt.Fprintf(rtx.Stdout, "hello, %s\n", flags.Name)
}

// ask writes the prompt to stderr, so stdout keeps only the command's output, and reads one line.
func ask(rtx *rotini.Context, prompt string) string {
	fmt.Fprint(rtx.Stderr, prompt)
	line, _ := bufio.NewReader(rtx.Stdin).ReadString('\n')
	return strings.TrimSpace(line)
}
{{< /code >}}

`--no-input` is a cascading root flag with `variable: PROMPTDEMO_NO_INPUT`, so a CI job can set
it once. For richer prompts (selects, confirmations, validation as you type), use a prompt
library such as `charm.land/huh/v2` in place of `ask`:
`huh.NewInput().Title("Name?").Value(&flags.Name).Run()`. Never echo a secret as it is typed:
read it with `term.ReadPassword` from `golang.org/x/term`.

### Update notices and self-update

An update notice is a few lines in the root handler's `CascadingPostRun`, which runs after every
command. Keep it out of the way: check at most once a day, give the check a short timeout, write
only to stderr, stay quiet when stderr isn't a terminal or `CI` is set, and declare a variable
that turns it off. Both variables are env inputs in the spec. Remove `rotini.NoCascadingPostRun`
from the root handler and add:

{{< code title="internal/cmd/updemo/notify.go" language="golang" open="true" collapsible="false" copy="true" >}}
package updemo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-rotini/rotini"
)

// These are variables so a test can replace them.
var (
	latestVersion = fetchLatestVersion
	isTerminal    = rotini.IsTerminal
)

// CascadingPostRun runs after every command. At most once a day, and only for a person at a
// terminal, it says on stderr that a newer release exists.
func (*updemoHandler) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
	in, err := rtx.Inputs[UpdemoInputs]()
	if err != nil || in.Updemo.Env.NoUpdateNotifier || in.Updemo.Env.Ci != "" || !isTerminal(rtx.Stderr) {
		return
	}
	dirs, err := rotini.AppDirs(rtx, "updemo", "xdg")
	if err != nil {
		return
	}
	stamp := filepath.Join(dirs.Cache, "update-check")
	if info, err := os.Stat(stamp); err == nil && time.Since(info.ModTime()) < 24*time.Hour {
		return
	}
	// Record the check before making it, so a failing check isn't retried on every run.
	if os.MkdirAll(dirs.Cache, 0o700) != nil || os.WriteFile(stamp, nil, 0o600) != nil {
		return
	}
	latest, err := latestVersion(ctx)
	if err != nil || latest == "" || latest == rtx.Version() {
		return
	}
	fmt.Fprintf(rtx.Stderr, "A new release of updemo is available: %s → %s\n", rtx.Version(), latest)
	fmt.Fprintln(rtx.Stderr, "Set UPDEMO_NO_UPDATE_NOTIFIER=1 to stop these notices.")
}

// fetchLatestVersion asks the release server for the newest version, giving up after two
// seconds so a slow network never holds up the command.
func fetchLatestVersion(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/updemo/latest", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("release server: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	return strings.TrimSpace(string(body)), err
}
{{< /code >}}

`latest == rtx.Version()` only tells versions apart; compare them with
`golang.org/x/mod/semver` (which wants a leading `v`) to skip a server that is behind.

Replacing the binary is a command you declare, such as `self-update`, never something a run
does on its own. `github.com/creativeprojects/go-selfupdate` finds the newest GitHub release
and swaps the running executable. It checks the download against the release's checksums file
only when given a validator:

{{< code title="self-update (sketch)" language="golang" open="true" collapsible="false" copy="true" >}}
up, err := selfupdate.NewUpdater(selfupdate.Config{
	Validator: &selfupdate.ChecksumValidator{UniqueFilename: "checksums.txt"},
})
if err != nil {
	rtx.HaltWith(err)
	return
}
rel, err := up.UpdateSelf(ctx, rtx.Version(), selfupdate.ParseSlug("me/updemo"))
if err != nil {
	rtx.HaltWith(err)
	return
}
fmt.Fprintln(rtx.Stderr, "updated to", rel.Version())
{{< /code >}}

A binary installed by a package manager should be updated by that package manager; leave the
command out of those builds, or have it print the upgrade command instead.
