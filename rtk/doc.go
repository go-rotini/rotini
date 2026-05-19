// Package rtk is the rotini-runtime toolkit — the single Go package
// generated CLIs import to get every swappable rotini-runtime service plus
// the shared types every CLI uses uniformly.
//
// Generated code imports rtk for:
//
//   - Registry / Bind / Has / Get[T] / Ctx — the service registry and
//     per-command context.
//   - Parser / Target / Result / ProgramSpec / CommandSpec / FlagSpec /
//     ArgumentSpec / StdinSpec / Inputs — the parser and its data model.
//   - IO / OS / Signals / Ticker — swappable default services. Over time:
//     Term, Output, Prompt, Progress, Exec, HTTPX, Daemon.
//
// All services are swappable via the registry. Generated code auto-binds
// the rtk defaults during Program.Execute; users override by binding a
// different implementation under the same key in main.go.
//
// See `.docs/ROTINI_PACKAGE_REQUIREMENTS.md` §5 for the full public API
// surface, §8 for the end-to-end walkthrough, and §15 for the milestone
// implementation plan.
package rtk
