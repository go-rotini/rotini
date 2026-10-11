package shape

import "io"

// Render adapts t to WriteOutput's and WriteOutputItem's renderer: it executes t over the value
// and ends what it writes with a newline, so a stream gets one line (or more) per item. It
// ignores the format it is passed.
//
//	rtx.WriteOutput(list, "template", shape.Render[TodoListOutput](in.Flags.Format))
func Render[T any](t Template) func(io.Writer, string, T) error {
	return func(w io.Writer, _ string, v T) error {
		if err := t.Execute(w, v); err != nil {
			return err
		}
		_, err := io.WriteString(w, "\n")
		return err //nolint:wrapcheck // the writer's own error
	}
}
