// Package internal is rotini's private codegen engine. It loads the user's
// .rotini.spec.yaml (or .json / .toml / .jsonc), validates it against the
// embedded JSON Schema, translates the spec DSL into a [rtk.ProgramSpec]-
// shaped Go value, and renders the per-concern templates that emit the
// user's *.gen.go files.
//
// This package is intentionally module-internal: the rotini binary in
// cmd/rotini uses it, and nothing else should. External tools that need to
// validate or generate from a rotini spec should invoke `go tool rotini ...`
// rather than importing this package.
//
// Why internal vs. exported? The codegen layer rewrites user code on every
// run; the public contract is the *.rotini.spec.yaml schema and the
// generated *.gen.go file shape, not the Go API used to produce them. Locking
// the API down lets us iterate on rendering details without breaking semver
// promises.
//
// The main entry points are:
//
//   - [LoadSpec]      — parse a spec file in any of the supported formats.
//   - [Validate]      — JSON-Schema + semantic validation of a loaded spec.
//   - [Run]           — full emission flow: load → validate → translate →
//     render → write.
//   - [Initialize]    — scaffolds a new rotini project (.rotini.spec.yaml,
//     .rotini.conf.yaml, main.go).
//   - [Prune]         — deletes handler files for commands that were removed
//     from the spec.
//
// All file writes go through go-rotini/fs's atomic-write primitives so an
// interrupted codegen run never leaves a half-written file on disk.
package internal
