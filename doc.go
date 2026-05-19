// Package main is the entrypoint for the rotini binary — the codegen tool for
// rotini-generated CLIs.
//
// Users install rotini via `go get -tool github.com/go-rotini/rotini` and
// invoke it via `go tool rotini init`, `go tool rotini generate`,
// `go tool rotini validate`, etc. See `.docs/ROTINI_PACKAGE_REQUIREMENTS.md`
// for the full package contract and usage walkthrough.
//
// rotini is itself a rotini-generated CLI: its seven subcommands (init,
// generate, validate, help, version, completion, root) are dispatched by
// the framework code in internal/rotini and implemented in
// internal/handlers — both regenerated from .rotini.spec.yaml.
//
// Generated CLIs do not depend on this `main` package — they depend on the
// exported sub-package github.com/go-rotini/rotini/rtk, which holds the
// runtime toolkit (parser, IO, OS, signals, ticker, registry, ctx).
package main
