package codegen

import "strings"

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
