// Package rtk is rotini's opt-in toolkit: rotini-flavored capabilities a CLI can
// choose to use on top of the core runtime, but does not have to. Core rotini (the
// github.com/go-rotini/rotini package) owns the framework essentials — codegen,
// command resolution + lifecycle dispatch, and turning argv/env/config/stdin into the
// generated input structs ([rotini.Parser] / [rotini.Binder]). What lives here is the
// rest, each piece independently adoptable:
//
//   - [CompletionScript] / [InstallCompletion] — generate and install bash/zsh/fish/
//     powershell shell-completion scripts for a program.
//   - [Signals] — register per-signal handlers, armed and torn down from lifecycle hooks.
//   - [Tickers] — a named registry of scheduled callbacks, on the same hook discipline.
//
// A handler reaches for rtk when it wants these conventions; a handler that disagrees
// ignores rtk entirely.
package rtk
