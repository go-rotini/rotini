//go:build !cfgedittoml

package cfgedit

import "fmt"

// newTOMLEditor reports that TOML can't be written yet: splicing needs the source spans that
// go-rotini/toml v1.2.0 adds.
func newTOMLEditor() (editor, error) {
	return nil, fmt.Errorf("writing TOML config files isn't supported yet: %w", ErrUnsupported)
}
