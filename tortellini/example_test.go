package tortellini

import "fmt"

// Suggestor turns a parse error's offending token and candidate vocabulary
// into "did you mean" suggestions.
func ExampleSuggestor_Suggest() {
	s := NewSuggestor()
	fmt.Println(s.Suggest("delpoy", []string{"deploy", "destroy", "version"}))
	// Output: [deploy]
}
