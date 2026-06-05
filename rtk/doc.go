// Package rtk is rotini's opt-in toolkit: rotini-flavored capabilities a CLI can
// choose to use on top of the core runtime, but does not have to. Core rotini (the
// github.com/go-rotini/rotini package) owns the framework essentials — codegen,
// command resolution + lifecycle dispatch, turning argv/env/config/stdin into the
// generated input structs ([rotini.Parser] / [rotini.Binder]), and shell-completion
// script generation ([rotini.CompletionScript]). What lives here is [Signals] — a
// per-signal handler registry, armed and torn down from a handler's lifecycle hooks.
//
// A handler reaches for rtk when it wants this; a handler that disagrees ignores it.
package rtk
