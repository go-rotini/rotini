package codegen

// Duplicate keys: a key written twice in one mapping of a spec or conf. The YAML and JSON
// decoders keep one of the two (or merge them) silently, so the document would mean something
// its author didn't write. This pass finds them before decoding and reports each one where it
// stands.

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

// duplicateKeysError carries one positioned problem per repeated key. It unwraps to them, so
// each prints as its own line.
type duplicateKeysError struct{ problems []error }

func (e *duplicateKeysError) Error() string   { return errors.Join(e.problems...).Error() }
func (e *duplicateKeysError) Unwrap() []error { return e.problems }

// duplicateMsg is the fix every duplicate-key problem names.
const duplicateMsg = "the second would replace the first, so remove one"

// checkDuplicateKeys returns a *duplicateKeysError for a YAML, JSON or JSONC document that
// repeats a key in one mapping, or nil. kind is "spec" or "conf"; path labels each problem. A
// document that does not parse is left for the decoder to report.
func checkDuplicateKeys(kind string, format fileFormat, data []byte, path string) error {
	var problems []error
	switch format {
	case formatYAML:
		if f, err := yaml.Parse(data); err == nil {
			for _, doc := range f.Docs {
				problems = append(problems, yamlDuplicates(kind, path, doc, "")...)
			}
		}
	case formatJSON, formatJSONC:
		// JSON parses as JSONC, whose parser stops at the first repeated key.
		var de *jsonc.DuplicateKeyError
		if _, err := jsonc.Parse(data); errors.As(err, &de) {
			problems = append(problems, keyProblem(kind, path, de.Key, de.Pos.Line, de.Pos.Column))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return &duplicateKeysError{problems: problems}
}

// tomlDuplicateKey turns the TOML decoder's duplicate-key error into a positioned problem, or
// returns nil for any other error. The decoder stops at the first repeat.
func tomlDuplicateKey(kind string, err error, path string) error {
	var de *toml.DuplicateKeyError
	if !errors.As(err, &de) || de.Key == "" {
		return nil
	}
	return &duplicateKeysError{problems: []error{keyProblem(kind, path, de.Key, de.Pos.Line, de.Pos.Column)}}
}

// keyProblem reports a repeated key known only by name and position.
func keyProblem(kind, path, key string, line, col int) error {
	return &problem{kind: kind, loc: fmt.Sprintf("key %q", key), pos: fmt.Sprintf("%s:%d:%d", path, line, col),
		msg: "repeats a key of the same mapping; " + duplicateMsg}
}

// yamlDuplicates walks a YAML node, reporting each scalar key that repeats one before it in the
// same mapping, at its JSON pointer. A merge key (<<) is skipped: overriding a merged key is how
// merges work.
func yamlDuplicates(kind, path string, n *yaml.Node, ptr string) []error {
	if n == nil {
		return nil
	}
	var out []error
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Children {
			out = append(out, yamlDuplicates(kind, path, c, ptr)...)
		}
	case yaml.MappingNode:
		first := map[string]int{}
		for i := 0; i+1 < len(n.Children); i += 2 {
			k, v := n.Children[i], n.Children[i+1]
			if k.MergeKey || k.Kind != yaml.ScalarNode {
				continue
			}
			child := ptr + "/" + escapePointer(k.Value)
			if line, seen := first[k.Value]; seen {
				out = append(out, &problem{kind: kind, loc: child, pos: fmt.Sprintf("%s:%d:%d", path, k.Pos.Line, k.Pos.Column),
					msg: fmt.Sprintf("duplicate key %q (first at line %d); %s", k.Value, line, duplicateMsg)})
			} else {
				first[k.Value] = k.Pos.Line
			}
			out = append(out, yamlDuplicates(kind, path, v, child)...)
		}
	case yaml.SequenceNode:
		for i, c := range n.Children {
			out = append(out, yamlDuplicates(kind, path, c, ptr+"/"+strconv.Itoa(i))...)
		}
	}
	return out
}

// docKind names a decoded document type in problems: "conf" for *Conf, else "spec".
func docKind(doc any) string {
	if _, ok := doc.(*Conf); ok {
		return "conf"
	}
	return "spec"
}
