package codegen

import (
	"fmt"
	"slices"
	"strings"
)

// exampleUse is what the words of one example set, read once the runtime parser has accepted
// them: the commands they run, each flag they give with its spelling and value, and the leaf
// command's positional words.
type exampleUse struct {
	chain []*Command // the commands it runs, the root first
	words []string   // the word that ran each command after the root, as typed
	flags []exampleFlagUse
	args  []exampleWord // the leaf command's positional words, in order
}

// exampleFlagUse is one flag an example gives.
type exampleFlagUse struct {
	flag     *FlagInput
	spelling string       // the identifier as typed (--json, -s, --no-color)
	value    *exampleWord // the value word; nil for a bool, a count or an implicit value
	negated  bool         // the negated form was typed
}

// readExample reads argv's words against the command tree under root. It reports false when
// a word can't be read with certainty: an unknown flag, or a command composed from another
// spec or a plugin, whose words belong to another program.
func readExample(root *Command, argv []exampleWord) (exampleUse, bool) {
	u := exampleUse{chain: []*Command{root}}
	cur := root
	raw := root.Passthrough // every later word is an argument
	for i := 0; i < len(argv); i++ {
		w := argv[i]
		switch {
		case raw:
			u.args = append(u.args, w)
		case w.text == "--":
			raw = true
		case strings.HasPrefix(w.text, "-") && w.text != "-":
			if w.opaque {
				return u, false
			}
			n, ok := u.readFlag(argv, i)
			if !ok {
				return u, false
			}
			i += n
		default:
			if len(u.args) == 0 && !w.opaque {
				if exampleRunsPlugin(cur, w.text) {
					return u, false
				}
				if child := exampleChild(cur, w.text); child != nil {
					if child.Ref != "" {
						return u, false
					}
					cur = child
					u.chain = append(u.chain, child)
					u.words = append(u.words, w.text)
					raw = child.Passthrough
					continue
				}
			}
			u.args = append(u.args, w)
			raw = cur.OptionsFirst
		}
	}
	return u, true
}

// exampleChild returns c's sub-command that word runs, or nil.
func exampleChild(c *Command, word string) *Command {
	for i := range c.Commands {
		if slices.Contains(dispatchTokens(&c.Commands[i]), word) {
			return &c.Commands[i]
		}
	}
	return nil
}

// exampleRunsPlugin reports whether word names one of c's declared plugins.
func exampleRunsPlugin(c *Command, word string) bool {
	return slices.ContainsFunc(c.Plugins, func(pl PluginSpec) bool { return pl.Name == word || slices.Contains(pl.Aliases, word) })
}

// readFlag reads the flag word at argv[i] and returns how many of the words after it the flag consumed. The
// nearest command declaring the spelling owns it, as on the command line.
func (u *exampleUse) readFlag(argv []exampleWord, i int) (int, bool) {
	word := argv[i].text
	name, attached, hasValue := strings.Cut(word, "=")
	if f, negated := u.flagSpelled(name); f != nil {
		use := exampleFlagUse{flag: f, spelling: name, negated: negated}
		consumed := 0
		switch {
		case hasValue:
			use.value = &exampleWord{text: attached, opaque: argv[i].opaque}
		case exampleTakesValue(f) && !negated:
			if i+1 >= len(argv) {
				return 0, false
			}
			use.value = &argv[i+1]
			consumed = 1
		}
		u.flags = append(u.flags, use)
		return consumed, true
	}
	// A short identifier with its value attached (-sopen), or a cluster of short ones (-vq).
	if strings.HasPrefix(word, "--") || len(word) < 3 {
		return 0, false
	}
	rest := word[1:]
	for rest != "" {
		short := "-" + rest[:1]
		f, _ := u.flagSpelled(short)
		if f == nil {
			return 0, false
		}
		rest = rest[1:]
		use := exampleFlagUse{flag: f, spelling: short}
		if exampleTakesValue(f) {
			if rest == "" {
				if i+1 >= len(argv) {
					return 0, false
				}
				use.value = &argv[i+1]
				u.flags = append(u.flags, use)
				return 1, true
			}
			use.value = &exampleWord{text: strings.TrimPrefix(rest, "=")}
			u.flags = append(u.flags, use)
			return 0, true
		}
		u.flags = append(u.flags, use)
	}
	return 0, true
}

// flagSpelled returns the flag of the commands run so far, the nearest first, that spelling
// names, and whether spelling is its negated form.
func (u *exampleUse) flagSpelled(spelling string) (*FlagInput, bool) {
	for _, c := range slices.Backward(u.chain) {
		for j := range c.Flags {
			f := &c.Flags[j]
			if slices.Contains(matchIdentifiers(*f), spelling) {
				return f, false
			}
			if slices.Contains(negatedForms(*f), spelling) {
				return f, true
			}
		}
	}
	return nil, false
}

// exampleTakesValue reports whether a flag reads the next word as its value when none is
// attached.
func exampleTakesValue(f *FlagInput) bool {
	return !isBoolFlag(*f) && !isCountFlag(*f) && (f.Schema == nil || f.Schema.ImplicitValue == nil)
}

// leaf is the command the example runs.
func (u *exampleUse) leaf() *Command { return u.chain[len(u.chain)-1] }

// arguments pairs the leaf command's positional words with its arguments, as the runtime binds
// them: in order, a variadic argument taking what the fixed ones around it leave.
func (u *exampleUse) arguments() []exampleArgUse {
	args := u.leaf().Arguments
	n := len(u.args)
	v := slices.IndexFunc(args, func(a ArgumentInput) bool { return strings.HasPrefix(getSchemaType(a.Schema), "[]") })
	spans := make([][2]int, len(args))
	if v < 0 {
		for i := range args {
			spans[i] = [2]int{min(i, n), min(i+1, n)}
		}
	} else {
		for i := range v {
			spans[i] = [2]int{min(i, n), min(i+1, n)}
		}
		lead, tail := min(v, n), len(args)-v-1
		for j := range tail {
			at := n - (tail - j)
			if at < lead {
				spans[v+1+j] = [2]int{lead, lead}
				continue
			}
			spans[v+1+j] = [2]int{at, at + 1}
		}
		spans[v] = [2]int{lead, max(lead, n-tail)}
	}
	var out []exampleArgUse
	for i := range args {
		if s := spans[i]; s[1] > s[0] {
			out = append(out, exampleArgUse{arg: &args[i], values: u.args[s[0]:s[1]]})
		}
	}
	return out
}

// exampleArgUse is one argument an example gives, with its words.
type exampleArgUse struct {
	arg    *ArgumentInput
	values []exampleWord
}

// notes returns what the example teaches that a user shouldn't type: a hidden alias,
// identifier or value, or a literal value for a secret input. Each finishes a sentence that
// starts `example "<line>"`.
func (u *exampleUse) notes() []string {
	var out []string
	for i, word := range u.words {
		c := u.chain[i+1]
		if word != c.Name && !slices.Contains(c.Aliases, word) {
			out = append(out, fmt.Sprintf("uses %q, a hidden alias of command %q; show a listed name (%s)", word, c.Name, strings.Join(append([]string{c.Name}, c.Aliases...), ", ")))
		}
	}
	for _, fu := range u.flags {
		f := fu.flag
		if !fu.negated && !slices.Contains(flagIdentifiers(*f), fu.spelling) {
			out = append(out, fmt.Sprintf("uses %q, a hidden identifier of flag %q; show a listed one (%s)", fu.spelling, f.Name, strings.Join(flagIdentifiers(*f), ", ")))
		}
		if fu.value == nil {
			continue
		}
		if v := hiddenEnumValue(f.Schema, *fu.value); v != "" {
			out = append(out, fmt.Sprintf("gives flag %q the hidden value %q; show a listed value", f.Name, v))
		}
		if exampleSecretLiteral(f.Schema, *fu.value) {
			out = append(out, fmt.Sprintf("gives the secret flag %q a literal value, which would show in the process list and shell history; write a placeholder instead (%s <%s>)", f.Name, fu.spelling, f.Name))
		}
	}
	for _, au := range u.arguments() {
		for _, w := range au.values {
			if v := hiddenEnumValue(au.arg.Schema, w); v != "" {
				out = append(out, fmt.Sprintf("gives argument <%s> the hidden value %q; show a listed value", au.arg.Name, v))
			}
			if exampleSecretLiteral(au.arg.Schema, w) {
				out = append(out, fmt.Sprintf("gives the secret argument <%s> a literal value, which would show in the process list and shell history; write a placeholder instead (<%s>)", au.arg.Name, au.arg.Name))
			}
		}
	}
	return out
}

// hiddenEnumValue returns the item of w, a value of an input with schema s, that names a
// hidden enum value (by its spelling or an alias), or "".
func hiddenEnumValue(s *InputSchema, w exampleWord) string {
	if s == nil || w.opaque || !enumDetailed(s.Enum) {
		return ""
	}
	items := []string{w.text}
	if s.Separator != "" {
		items = strings.Split(w.text, runtimeSeparator(s.Separator))
	}
	same := func(a, b string) bool { return a == b || (s.IgnoreCase && strings.EqualFold(a, b)) }
	for _, item := range items {
		for _, v := range enumValues(s.Enum) {
			if v.Hidden && (same(item, v.Value) || slices.ContainsFunc(v.Aliases, func(a string) bool { return same(item, a) })) {
				return item
			}
		}
	}
	return ""
}

// exampleSecretLiteral reports whether w gives a secret input a value typed on the command
// line: not opaque, and not one of the markers its `from` declares (@path, -).
func exampleSecretLiteral(s *InputSchema, w exampleWord) bool {
	if s == nil || !s.Secret || w.opaque {
		return false
	}
	if slices.Contains(s.From, "file") && strings.HasPrefix(w.text, "@") && !strings.HasPrefix(w.text, "@@") {
		return false
	}
	return !slices.Contains(s.From, "stdin") || w.text != "-"
}
