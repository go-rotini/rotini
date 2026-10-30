package codegen

import (
	"slices"
	"strings"
)

// globRule is the man and markdown note on a `glob: true` argument.
const globRule = "patterns (*, ?, [...]) are expanded on Windows"

// withStreamNote appends what "-" means to the summary of an inputfile or outputfile input.
func withStreamNote(summary string, schema *InputSchema) string {
	stream := map[string]string{"inputfile": "stdin", "outputfile": "stdout"}[streamPathKind(schema)]
	if stream == "" {
		return summary
	}
	return strings.TrimSpace(summary + " (- for " + stream + ")")
}

// withSecretNote appends how to pass a secret that refuses a typed value to its summary: the
// forms its `from` declares, "(@file or - for stdin)".
func withSecretNote(summary string, schema *InputSchema) string {
	if !literalFreeSecret(schema) {
		return summary
	}
	var forms []string
	if slices.Contains(schema.From, "file") {
		forms = append(forms, "@file")
	}
	if slices.Contains(schema.From, "stdin") {
		forms = append(forms, "- for stdin")
	}
	if len(forms) == 0 {
		return summary
	}
	return strings.TrimSpace(summary + " (" + strings.Join(forms, " or ") + ")")
}
