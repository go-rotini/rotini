package rotini

import (
	"encoding/json"
	"fmt"
)

// SelectFields keeps the fields a `--json` flag names on each item at the path given, and keeps
// everything outside it. Sort first: the sort field need not be selected.
func ExampleSelectFields() {
	type task struct {
		ID       int    `json:"id"`
		Title    string `json:"title"`
		Priority int    `json:"priority"`
	}
	type taskList struct {
		Tasks []task `json:"tasks"`
		Total int    `json:"total"`
	}
	list := taskList{Tasks: []task{{1, "write docs", 2}, {2, "ship", 1}}, Total: 2}

	if err := SortBy(list.Tasks, "priority", false); err != nil {
		fmt.Println(err)
		return
	}
	sel, err := SelectFields(list, "tasks", []string{"id", "title"})
	if err != nil {
		fmt.Println(err)
		return
	}
	out, _ := json.Marshal(sel)
	fmt.Println(string(out))
	// Output:
	// {"tasks":[{"id":2,"title":"ship"},{"id":1,"title":"write docs"}],"total":2}
}

func ExampleStripANSI() {
	fmt.Printf("%q\n", StripANSI("\x1b[1;31merror\x1b[0m: \x1b]8;;https://example.com\x07link\x1b]8;;\x07"))
	// Output:
	// "error: link"
}
