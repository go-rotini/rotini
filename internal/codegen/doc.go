// Package codegen is rotini's compile-time engine: it turns an end-user's CLI definition
// (a .rotini.spec + .rotini.conf) into a working Go program. It is, in effect, a small
// compiler — and the package is laid out as that compiler's pipeline.
//
// # The spine
//
// The Processor is the through-line. It holds rotini's OWN spec + conf JSON Schemas (the
// definition of what a valid CLI looks like), and runs an end-user's files — their
// IMPLEMENTATION — through the stages:
//
//		reconcile → validate → lint → generate
//
//	  - reconcile  read + decode the user's spec + conf files into the in-memory model.
//	  - validate   check that model against rotini's schemas, and its declared version
//	    against this binary.
//	  - lint       apply rotini's rules — the constraints a JSON Schema can't express.
//	  - generate   resolve the model into a program and emit it.
//
// Reading Processor.Generate / Processor.Validate is reading the whole process.
//
// # The file layout IS the pipeline
//
// Every file belongs to exactly one stage; its name says which:
//
//	processor*     the Processor + the run/watch loop (the spine)
//	schema*        rotini's JSON Schemas, the generated model types, and reading/walking them
//	reconcile*     reading + decoding the user's spec/conf files, with source positions
//	validate*      schema + version validation, and the problem (finding) type
//	lint*          rotini's lint rules (spec + conf), composition, suggestions
//	generate*      the generate stage (see below)
//	initialize.go  `rotini initialize` — scaffold a new CLI, then run the same generate
//
// # The generate stage has its own spine
//
// A spec + conf resolve into a [program] — the command tree, the output layout (where each
// generated file goes), and the module it is written into. The program's generate() method
// runs the emit steps in order:
//
//	emit schemas → emit runtime → emit cmd file → emit feature pages →
//	emit handler stubs → emit entrypoint → prune orphans → audit handler hooks
//
// The last step is the only one that READS the author's code rather than writing rotini's: it
// reports a method on a handler type whose name is a near-miss of a lifecycle hook, the one way
// a hook can go wrong that the generated `var _ rotini.Handlers` assertion cannot catch.
//
// Read program.generate() and you have read, top to bottom, exactly what `rotini generate`
// does. The generate_* renderers (literals, schema codegen, templates, the runtime merge)
// are the tools those steps call.
package codegen
