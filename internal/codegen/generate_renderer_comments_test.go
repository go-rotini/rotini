package codegen

import (
	"strings"
	"testing"
)

// A comment above a key holding a mapping stays with that key when the seed is transcoded:
// TOML writes it above the key's table. A comment above the mapping's first key stays there.
func TestConvert_commentAboveMappingKey(t *testing.T) {
	src := []byte("command:\n  name: x\n  # how to install the completer\n  multicall:\n    complete: k_complete-\n  other:\n    # the inner key\n    inner: 1\n")
	comments, err := seedComments(src)
	if err != nil {
		t.Fatal(err)
	}
	if got := comments["command.multicall"]; len(got) != 1 || got[0] != "how to install the completer" {
		t.Errorf("comments[command.multicall] = %q", got)
	}
	if got := comments["command.other.inner"]; len(got) != 1 || got[0] != "the inner key" {
		t.Errorf("comments[command.other.inner] = %q", got)
	}
	out, err := convert(src, formatTOML)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "# how to install the completer\n[command.multicall]\n") {
		t.Errorf("toml =\n%s\nwant the comment above [command.multicall]", out)
	}
}
