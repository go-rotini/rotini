//go:build !cfgedittoml

package cfgedit

import (
	"errors"
	"testing"
)

func TestTOMLUnsupported(t *testing.T) {
	_, err := Set([]byte("a = 1\n"), TOML, []string{"a"}, int64(2))
	if !errors.Is(err, ErrUnsupported) || err.Error() != "writing TOML config files isn't supported yet: unsupported format" {
		t.Fatalf("Set: %v", err)
	}
	if _, err := Unset([]byte("a = 1\n"), TOML, []string{"a"}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Unset: %v", err)
	}
}
