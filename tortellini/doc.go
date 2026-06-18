// Package tortellini is rotini's opt-in services subpackage: small,
// dependency-free, framework-agnostic helpers a program binds into the service
// registry and a handler retrieves. The core framework decides nothing for the
// end-user — every convenience here is opt in.
//
// The services follow the bind-a-constructor convention; bind under the
// conventional key, retrieve by it:
//
//		program.Bind(tortellini.KeyStyler, tortellini.NewStyler())
//		program.Bind(tortellini.KeySuggestor, tortellini.NewSuggestor())
//		program.Bind(tortellini.KeyVersioner, tortellini.NewVersioner(version))
//
//	  - [Styler] / [Style] — fluent ANSI/SGR text styling: 16/256/RGB/hex color
//	    with opt-in [Profile] downsampling, a registry of named styles rendered by
//	    intent ("warning", "error"), and per-style or program-wide enable. A
//	    disabled Style passes text through; a disabled Styler strips, so a
//	    program-wide "no color" comes out clean.
//	  - [Suggestor] — "did you mean" ranking that turns a mistyped token and a
//	    vocabulary of candidate strings into ranked suggestions (e.g. after a
//	    rotini.ParseError), via a pluggable [SuggestAlgorithm].
//	  - [Versioner] — resolves the program's version into the forms a version
//	    handler prints (the full string and its leading semantic X.Y.Z), from the
//	    module's build info or an -ldflags fallback.
//
// All are usable in any Go program, not just a rotini CLI. The package also
// exposes a few standalone text utilities — [Strip], [Hyperlink], [Width] — and
// opt-in capability detectors — [DetectProfile], [IsTerminal], [EnvNoColor] — that
// the program consults to decide whether and how to style. Detection is never
// automatic: the program detects and decides, then feeds the result in.
package tortellini
