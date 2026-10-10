package rotini

import (
	"errors"
	"fmt"
	"os/user"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
)

// expandRule is what an input's `expand` and `relative_to` declare, read from the generated
// field's `expand:"home,env"` and `relativeto:"config"` tags.
type expandRule struct {
	home, env bool // expand ~ and ~name; expand $NAME and ${NAME}
	relConfig bool // a relative value from a configuration file joins onto the file's directory
}

// tagExpandRule reads an input field's expansion tags.
func tagExpandRule(tag reflect.StructTag) expandRule {
	var r expandRule
	for kind := range strings.SplitSeq(tag.Get("expand"), ",") {
		switch kind {
		case "home":
			r.home = true
		case "env":
			r.env = true
		}
	}
	r.relConfig = tag.Get("relativeto") == "config"
	return r
}

// expands reports whether the rule rewrites values at all.
func (r expandRule) expands() bool { return r.home || r.env }

// declared reports whether the rule does anything.
func (r expandRule) declared() bool { return r.expands() || r.relConfig }

// expandError is a value expansion could not complete. Its message names the problem and the
// value as written; the caller adds the input's label.
type expandError struct{ msg string }

func (e *expandError) Error() string { return e.msg }

// expandValue expands one input value by rule over the run's view: ~ first, then variables,
// in one pass, so text a variable supplies is never expanded again. Variables are read the way
// env inputs read them, .env files and variable_file values included.
func expandValue(v string, r expandRule, view *osView) (string, error) {
	return expandValueFor(runtime.GOOS, v, r, view, user.Lookup)
}

// expandValueFor is expandValue with the operating system and the user lookup given, for
// tests.
func expandValueFor(goos, v string, r expandRule, view *osView, lookupUser func(string) (*user.User, error)) (string, error) {
	home, rest := "", v
	if r.home {
		var err error
		if home, rest, err = expandHome(goos, v, view, lookupUser); err != nil {
			return "", err
		}
	}
	if r.env {
		var err error
		if rest, err = expandEnv(v, rest, view); err != nil {
			return "", err
		}
	}
	return home + rest, nil
}

// expandHome splits a value that is exactly ~, or starts with ~/ (or ~\ on Windows), into the
// home directory and the rest, and ~name/… into that user's home directory and the rest. A
// value with no leading ~ is all rest.
func expandHome(goos, v string, view *osView, lookupUser func(string) (*user.User, error)) (home, rest string, err error) {
	if !strings.HasPrefix(v, "~") {
		return "", v, nil
	}
	seps := "/"
	if goos == "windows" {
		seps = `/\`
	}
	end := strings.IndexAny(v, seps)
	if end < 0 {
		end = len(v)
	}
	if end == 1 {
		h, herr := view.home()
		if herr != nil {
			return "", "", &expandError{fmt.Sprintf("cannot expand %q: %s", v, homeReason(herr))}
		}
		return h, v[1:], nil
	}
	name := v[1:end]
	u, uerr := lookupUser(name)
	if uerr != nil || u.HomeDir == "" {
		return "", "", &expandError{fmt.Sprintf("cannot expand %q: no such user", "~"+name)}
	}
	return u.HomeDir, v[end:], nil
}

// homeReason is why the home directory is unknown, without the wrapping view.home adds.
func homeReason(err error) string {
	msg := err.Error()
	if rest, ok := strings.CutPrefix(msg, "home directory: "); ok {
		return rest
	}
	return msg
}

// expandEnv replaces $NAME and ${NAME} in s, where written is the value as the user wrote it,
// for messages. An unset or empty variable is an error naming it, and so is a braced
// reference with an operator (${A:-x}). A $ not followed by a name character or { is kept.
func expandEnv(written, s string, view *osView) (string, error) {
	if !strings.Contains(s, "$") {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '$' {
			b.WriteByte(s[i])
			i++
			continue
		}
		var name string
		next := i + 1
		switch {
		case next < len(s) && s[next] == '{':
			end := strings.IndexByte(s[next:], '}')
			if end < 0 {
				return "", &expandError{fmt.Sprintf("unclosed ${ in %q", written)}
			}
			name = s[next+1 : next+end]
			if !isVarName(name) {
				return "", &expandError{fmt.Sprintf("only $NAME and ${NAME} are expanded (in %q)", written)}
			}
			next += end + 1
		case next < len(s) && isVarFirstByte(s[next]):
			end := next + 1
			for end < len(s) && isVarByte(s[end]) {
				end++
			}
			name, next = s[next:end], end
		default:
			b.WriteByte('$')
			i++
			continue
		}
		val, _ := view.inputLookup(name)
		if val == "" {
			return "", &expandError{fmt.Sprintf("$%s is not set (in %q)", name, written)}
		}
		b.WriteString(val)
		i = next
	}
	return b.String(), nil
}

// isVarName reports whether s is a variable name: [A-Za-z_][A-Za-z0-9_]*.
func isVarName(s string) bool {
	if s == "" || !isVarFirstByte(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isVarByte(s[i]) {
			return false
		}
	}
	return true
}

// isVarByte reports whether c can continue a variable name.
func isVarByte(c byte) bool { return isVarFirstByte(c) || (c >= '0' && c <= '9') }

// expandValues expands each value by rule, returning the input unchanged (the same slice)
// when nothing changed. The error is the first failure, labeled.
func expandValues(vals []string, r expandRule, view *osView, label string) ([]string, error) {
	if !r.expands() {
		return vals, nil
	}
	var out []string
	for i, v := range vals {
		x, err := expandValue(v, r, view)
		if err != nil {
			return nil, labelExpandError(label, err)
		}
		if x != v && out == nil {
			out = make([]string, len(vals))
			copy(out, vals)
		}
		if out != nil {
			out[i] = x
		}
	}
	if out == nil {
		return vals, nil
	}
	return out, nil
}

// relativeToFile joins each relative value onto the directory of file, the configuration file
// that supplied them; with no file the values are returned as they are.
func relativeToFile(vals []string, file string) []string {
	if file == "" {
		return vals
	}
	dir := filepath.Dir(file)
	var out []string
	for i, v := range vals {
		if v == "" || filepath.IsAbs(v) || v == "-" {
			continue
		}
		if out == nil {
			out = make([]string, len(vals))
			copy(out, vals)
		}
		out[i] = filepath.Join(dir, v)
	}
	if out == nil {
		return vals
	}
	return out
}

// labelExpandError prefixes an expansion failure with the input's label.
func labelExpandError(label string, err error) error {
	if ee, ok := errors.AsType[*expandError](err); ok {
		return &expandError{label + ": " + ee.msg}
	}
	return err
}
