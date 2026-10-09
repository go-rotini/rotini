package shape_test

import (
	"fmt"
	"os"

	"github.com/go-rotini/rotini/shape"
)

type Task struct {
	ID    int      `json:"id"`
	Title string   `json:"title"`
	Tags  []string `json:"tags,omitempty"`
}

type TaskList struct {
	Tasks []Task `json:"tasks"`
}

func Example() {
	var format shape.Template
	if err := format.UnmarshalText([]byte(`{{range .tasks}}{{.id}} {{.title}} [{{join "," .tags}}]{{"\n"}}{{end}}`)); err != nil {
		fmt.Println(err)
		return
	}
	list := TaskList{Tasks: []Task{{ID: 1, Title: "write docs", Tags: []string{"docs"}}, {ID: 2, Title: "ship"}}}
	if err := format.Execute(os.Stdout, list); err != nil {
		fmt.Println(err)
	}
	// Output:
	// 1 write docs [docs]
	// 2 ship []
}

func ExampleRender() {
	var format shape.Template
	_ = format.UnmarshalText([]byte(`{{.title}}`))
	render := shape.Render[Task](format)
	for _, t := range []Task{{ID: 1, Title: "write docs"}, {ID: 2, Title: "ship"}} {
		_ = render(os.Stdout, "template", t)
	}
	// Output:
	// write docs
	// ship
}

func ExampleTemplate_Execute_missingKey() {
	var format shape.Template
	_ = format.UnmarshalText([]byte(`{{.Title}}`))
	fmt.Println(format.Execute(os.Stdout, Task{Title: "write docs"}))
	// Output:
	// template:1:2: executing "template" at <.Title>: map has no entry for key "Title"
}
