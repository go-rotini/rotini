package rotini

import (
	"slices"
	"strings"
)

// literalFree reports whether a secret input refuses a value typed on the command line: it
// declares acquisition modes and leaves out "value", so the secret comes from a file or stdin
// and never shows in the process list or shell history.
func literalFree(secret bool, from []string) bool {
	return secret && len(from) > 0 && !slices.Contains(from, "value")
}

// refuseSecretLiteral is the usage error for a literal argv value given to a literal-free
// secret input, or nil when value is one of the input's markers (`@path`, a bare "-"). label
// names the input as typed and flag is its flag label, "" for an argument. The value is never
// echoed.
func refuseSecretLiteral(secret bool, from []string, label, flag, value string) error {
	if !literalFree(secret, from) {
		return nil
	}
	file, stdin := slices.Contains(from, "file"), slices.Contains(from, "stdin")
	if (file && strings.HasPrefix(value, "@") && !strings.HasPrefix(value, "@@")) || (stdin && value == "-") {
		return nil
	}
	var forms []string
	if file {
		forms = append(forms, "@file")
	}
	if stdin {
		forms = append(forms, "-")
	}
	return &ParseError{Kind: ParseKindInvalidValue, Msg: label + " takes " + strings.Join(forms, " or ") + ", not a value", Flag: flag}
}

// secretTakesNoLiteral is why [ArgvOf] can't write a literal-free secret.
const secretTakesNoLiteral = "the secret refuses a value on the command line; pass it through a file or stdin"
