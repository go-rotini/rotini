//go:build !mutation

package e2e

import "testing"

// introBlocks pins the site intro's quick start to the files r13_guide_intro builds.
var introBlocks = []pinnedBlock{
	{".rotini.spec.yaml", "r13_guide_intro.txtar", "hello.spec.yaml.txt"},
	{"helloworld_hello.go", "r13_guide_intro.txtar", "hello.go.txt"},
}

func TestIntroBlocksMatchScripts(t *testing.T) {
	checkPinnedBlocks(t, "_index.md", introBlocks)
}
