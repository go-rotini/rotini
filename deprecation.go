package rotini

import (
	"fmt"
	"slices"
)

// Deprecation is a deprecated token found in this invocation's argv: the identifier used, the
// kind of input, its logical name, the spec's `deprecated:` message, and the releases the spec
// plans. It implements error so it can be recorded or printed directly.
type Deprecation struct {
	Kind       string // "flag", "argument" or "command"
	Name       string // the input's logical name (the flag/argument/command name)
	Identifier string // the token actually used on argv (e.g. "--conf", "build"); an argument's <name>
	Message    string // the spec's `deprecated:` message, when the input is deprecated as a whole
	Since      string // the spec's `deprecated_since:` release (X.Y.Z), or ""
	RemovedIn  string // the release that removes this token: its own planned removal, else the input's `removed_in:`, or ""
	// Value is the deprecated enum value as typed, when the deprecation is about the value given
	// rather than the input; Message is then the value's own message. Empty otherwise.
	Value string
}

// Error renders the deprecation notice as a single line, with the author's message when there
// is one: `flag "--conf" is deprecated: use --config`.
func (d Deprecation) Error() string {
	if d.Value != "" {
		if d.Message != "" {
			return fmt.Sprintf("%s %q value %q is deprecated: %s", d.Kind, d.Identifier, d.Value, d.Message)
		}
		return fmt.Sprintf("%s %q value %q is deprecated", d.Kind, d.Identifier, d.Value)
	}
	if d.Message != "" {
		return fmt.Sprintf("%s %q is deprecated: %s", d.Kind, d.Identifier, d.Message)
	}
	return fmt.Sprintf("deprecated %s identifier %q was used", d.Kind, d.Identifier)
}

// Deprecations returns each deprecated token this invocation used: a command invoked via a
// deprecated alias or deprecated as a whole, a flag set via a deprecated identifier, a deprecated
// argument given a value, or a deprecated enum value given to a flag or argument (with
// [Deprecation.Value] set). Rotini prints nothing; the handler decides what to do:
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
			out = append(out, Deprecation{
				Kind: "command", Name: frame.Name, Identifier: frame.Matched, Message: frame.Deprecated,
				Since: frame.DeprecatedSince, RemovedIn: removedIn(frame.Matched, frame.DeprecatedIdentifiersRemovedIn, frame.RemovedIn),
			})
		}
		if store == nil {
			continue // argv does not parse; the parse error is what the user sees
		}
		for _, fd := range frame.Flags {
			out = append(out, flagDeprecations(fd, store.scopes[i].used[fd.Name])...)
			out = append(out, valueDeprecations("flag", fd.Name, store.scopes[i].label(fd), flagEnum(fd), store.scopes[i].flags[fd.Name])...)
		}
	}
	// A deprecated argument reports when a value was supplied for it.
	if len(chain) > 0 {
		var supplied int
		if store != nil {
			supplied = len(store.scopes[len(chain)-1].args)
		}
		out = append(out, argumentDeprecations(chain[len(chain)-1], supplied)...)
		if store != nil {
			out = append(out, argumentValueDeprecations(chain[len(chain)-1], store.scopes[len(chain)-1].args)...)
		}
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

// removedIn is the release that removes token: its own planned removal when it has one, else
// the whole input's.
func removedIn(token string, byToken map[string]string, whole string) string {
	if v := byToken[token]; v != "" {
		return v
	}
	return whole
}

// flagDeprecations reports a flag set on argv through a deprecated identifier — or through any
// identifier when the flag as a whole is deprecated — once per identifier used, carrying the
// spec's message.
func flagDeprecations(fd FlagDef, used []string) []Deprecation {
	var out []Deprecation
	for _, id := range used {
		if deprecatedToken(id, fd.DeprecatedIdentifiers, fd.Deprecated) {
			out = append(out, Deprecation{
				Kind: "flag", Name: fd.Name, Identifier: id, Message: fd.Deprecated,
				Since: fd.DeprecatedSince, RemovedIn: removedIn(id, fd.DeprecatedIdentifiersRemovedIn, fd.RemovedIn),
			})
		}
	}
	return out
}

// quietParse tokenizes argv against chain the way the parser does, on a copy of the chain with
// value acquisition off, so asking what argv says never reads a file or stdin. nil when argv
// does not parse.
func quietParse(chain []Command, argv []string) *parsedInputs {
	store, err := quietTokens(chain, argv)
	if err != nil {
		return nil
	}
	return store
}

// quietTokens is quietParse that also returns the parse error.
func quietTokens(chain []Command, argv []string) (*parsedInputs, error) {
	quiet := make([]Command, len(chain))
	for i, frame := range chain {
		flags := make([]FlagDef, len(frame.Flags))
		for j, fd := range frame.Flags {
			fd.From = nil
			flags[j] = fd
		}
		frame.Flags = flags
		args := make([]ArgDef, len(frame.Arguments))
		for j, ad := range frame.Arguments {
			ad.From = nil
			args[j] = ad
		}
		frame.Arguments = args
		quiet[i] = frame
	}
	return parseArgvTokens(quiet, argv, argvAcq{})
}

// argumentDeprecations reports each deprecated argument of the leaf that argv supplied a value
// for, by position: supplied is how many positionals argv gave the leaf.
func argumentDeprecations(leaf Command, supplied int) []Deprecation {
	var out []Deprecation
	spans := argSpans(leaf.Arguments, supplied)
	for i, ad := range leaf.Arguments {
		if ad.Deprecated != "" && spans[i][0] < spans[i][1] {
			out = append(out, Deprecation{
				Kind: "argument", Name: ad.Name, Identifier: "<" + ad.Name + ">", Message: ad.Deprecated,
				Since: ad.DeprecatedSince, RemovedIn: ad.RemovedIn,
			})
		}
	}
	return out
}

// valueDeprecations reports each distinct deprecated enum value among an input's command-line
// values: a deprecated value, any alias of one, or a deprecated alias. identifier names the input
// as the user typed it.
func valueDeprecations(kind, name, identifier string, enum enumSet, vals []string) []Deprecation {
	if len(enum.described) == 0 {
		return nil
	}
	var out []Deprecation
	seen := map[string]bool{}
	for _, v := range vals {
		d, ok := enum.deprecatedUse(v)
		if !ok || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, Deprecation{Kind: kind, Name: name, Identifier: identifier, Message: d.Deprecated, Since: d.DeprecatedSince, RemovedIn: d.RemovedIn, Value: v})
	}
	return out
}

// argumentValueDeprecations reports the deprecated enum values among the leaf's positionals.
func argumentValueDeprecations(leaf Command, args []string) []Deprecation {
	var out []Deprecation
	spans := argSpans(leaf.Arguments, len(args))
	for i, ad := range leaf.Arguments {
		if s := spans[i]; s[0] < s[1] {
			out = append(out, valueDeprecations("argument", ad.Name, "<"+ad.Name+">", argEnum(ad), args[s[0]:s[1]])...)
		}
	}
	return out
}
