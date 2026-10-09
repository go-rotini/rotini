package rotini

import (
	"fmt"
	"slices"
)

// Deprecation is a deprecated token found in this invocation's argv: the identifier used, the
// kind of input, its logical name, and the spec's `deprecated:` message. It implements error so
// it can be recorded or printed directly.
type Deprecation struct {
	Kind       string // "flag", "argument" or "command"
	Name       string // the input's logical name (the flag/argument/command name)
	Identifier string // the token actually used on argv (e.g. "--conf", "build"); an argument's <name>
	Message    string // the spec's `deprecated:` message, when the input is deprecated as a whole
}

// Error renders the deprecation notice as a single line, with the author's message when there
// is one: `flag "--conf" is deprecated: use --config`.
func (d Deprecation) Error() string {
	if d.Message != "" {
		return fmt.Sprintf("%s %q is deprecated: %s", d.Kind, d.Identifier, d.Message)
	}
	return fmt.Sprintf("deprecated %s identifier %q was used", d.Kind, d.Identifier)
}

// Deprecations returns each deprecated token this invocation used: a command invoked via a
// deprecated alias or deprecated as a whole, a flag set via a deprecated identifier, or a
// deprecated argument given a value. Rotini prints nothing; the handler decides what to do:
//
//	for _, d := range rotini.Deprecations(rtx) {
//		rtx.RecordWarning(fmt.Errorf("%w — use %q instead", d, d.Name))
//	}
//
// It needs no [Parser]: it reads the resolved chain and argv from rtx, and never reads a file
// or stdin. It returns nil for a nil rtx. When argv does not parse, only command deprecations
// are reported.
func Deprecations(rtx *Context) []Deprecation {
	if rtx == nil {
		return nil
	}
	argv := rtx.Argv
	var out []Deprecation
	chain := rtx.CommandChain()
	store := quietParse(chain, argv)
	for i, frame := range chain {
		// A command deprecated as a whole reports however it was invoked; one with only
		// deprecated aliases reports when one of those resolved it (frame.Matched).
		if frame.Matched != "" && deprecatedToken(frame.Matched, frame.DeprecatedIdentifiers, frame.Deprecated) {
			out = append(out, Deprecation{Kind: "command", Name: frame.Name, Identifier: frame.Matched, Message: frame.Deprecated})
		}
		if store == nil {
			continue // argv does not parse; the parse error is what the user sees
		}
		for _, fd := range frame.Flags {
			out = append(out, flagDeprecations(fd, store.scopes[i].used[fd.Name])...)
		}
	}
	// A deprecated argument reports when a value was supplied for it.
	if len(chain) > 0 {
		var supplied int
		if store != nil {
			supplied = len(store.scopes[len(chain)-1].args)
		}
		out = append(out, argumentDeprecations(chain[len(chain)-1], supplied)...)
	}
	return out
}

// deprecatedToken reports whether using token deprecates: when some spellings are listed as
// deprecated, only those are — the others are the ones to move to, and the message (if any) is
// about the listed ones; with none listed, a message deprecates every spelling.
func deprecatedToken(token string, deprecatedIDs []string, message string) bool {
	if len(deprecatedIDs) > 0 {
		return slices.Contains(deprecatedIDs, token)
	}
	return message != ""
}

// flagDeprecations reports a flag set on argv through a deprecated identifier — or through any
// identifier when the flag as a whole is deprecated — once per identifier used, carrying the
// spec's message.
func flagDeprecations(fd FlagDef, used []string) []Deprecation {
	var out []Deprecation
	for _, id := range used {
		if deprecatedToken(id, fd.DeprecatedIdentifiers, fd.Deprecated) {
			out = append(out, Deprecation{Kind: "flag", Name: fd.Name, Identifier: id, Message: fd.Deprecated})
		}
	}
	return out
}

// quietParse tokenizes argv against chain the way the parser does, on a copy of the chain with
// value acquisition off, so asking what argv says never reads a file or stdin. nil when argv
// does not parse.
func quietParse(chain []Command, argv []string) *parsedInputs {
	quiet := make([]Command, len(chain))
	for i, frame := range chain {
		flags := make([]FlagDef, len(frame.Flags))
		for j, fd := range frame.Flags {
			fd.From = nil
			flags[j] = fd
		}
		frame.Flags = flags
		quiet[i] = frame
	}
	store, err := parseArgvTokens(quiet, argv, argvAcq{})
	if err != nil {
		return nil
	}
	return store
}

// argumentDeprecations reports each deprecated argument of the leaf that argv supplied a value
// for, by position: supplied is how many positionals argv gave the leaf.
func argumentDeprecations(leaf Command, supplied int) []Deprecation {
	var out []Deprecation
	for i, ad := range leaf.Arguments {
		if ad.Deprecated != "" && i < supplied {
			out = append(out, Deprecation{Kind: "argument", Name: ad.Name, Identifier: "<" + ad.Name + ">", Message: ad.Deprecated})
		}
	}
	return out
}
