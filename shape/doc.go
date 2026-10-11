// Package shape formats a command's output with a Go template the end-user passes, as in
// `--format '{{.title}}'`.
//
// Declare the flag with [Template] as its type, through the spec's `import:` key:
//
//	flags:
//	  - name: format
//	    summary: format the output with a Go template
//	    schema: { type: shape.Template, import: github.com/go-rotini/rotini/shape, placeholder: TEMPLATE }
//
// A template that does not parse is a usage error when the flag is read. The handler passes
// [Render] to WriteOutput or WriteOutputItem as the renderer:
//
//	if !in.Flags.Format.IsZero() {
//	    return rtx.WriteOutput(list, "template", shape.Render[TodoListOutput](in.Flags.Format))
//	}
//
// # Data
//
// A template runs over the output as its JSON encoding sees it, so it names fields by their
// JSON property names, the names the contract and `-o json` show: `{{.title}}`, not `{{.Title}}`.
// Objects are maps, lists are slices, numbers are [encoding/json.Number] (printed exactly as
// JSON prints them) and a value with its own JSON or text encoding, such as a time.Time, is
// that encoding. Every field a struct declares is present, an omitempty field included as its
// zero value, so a template that names a field the output type does not have is a usage error,
// and one that names an empty field is not.
//
// # Functions
//
// Besides text/template's builtins (printf, len, index, eq, …), a template can call:
//
//	json  the compact JSON of a value:   {{json .labels}}
//	join  a list's items joined by sep:  {{join ", " .tags}}
//
// # Security
//
// The template is end-user input that the program runs. It sees only the value passed to
// Execute or Render, already reduced to maps, slices, strings, numbers and bools, so it can call
// no method of the program's types, and its functions reach no file, environment, process or
// network. Render outputs, not inputs: the inputs value may hold secrets.
//
// # Binary size
//
// Importing this package links text/template, which turns off the linker's removal of unused
// methods for the whole program, so the binary grows. A program that does not import it links
// no template code.
package shape
