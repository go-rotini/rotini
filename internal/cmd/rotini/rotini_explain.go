package rotini

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

var _ rotini.Handler = (*rotiniExplainHandler)(nil)

type rotiniExplainHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniExplainHandler) Run(_ context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[RotiniExplainInputs]()
	if err != nil {
		haltWithInputError(rtx, err)
		return
	}
	rtx.SetDependencyIfAbsent(explainDep, codegen.NewProcessor(rtx.Version()).Explain)
	explain := rtx.MustGetDependency(explainDep)
	infos, err := explain(inputs.RotiniExplain.Arguments.Key)
	if err != nil {
		rtx.HaltWith(rotini.UsageError(err))
		return
	}
	var b strings.Builder
	for i, info := range infos {
		if i > 0 {
			b.WriteString("\n")
		}
		writeKeyInfo(&b, info)
	}
	if _, err := fmt.Fprint(rtx.Stdout, b.String()); err != nil {
		haltWithWriteError(rtx, err)
	}
}

// CompleteArgValue completes a key path one segment at a time: a key with keys below it ends
// in ".", and the shell adds no space after it, so the next segment follows.
func (*rotiniExplainHandler) CompleteArgValue(rtx *rotini.Context, _, partial string) []string {
	candidates := codegen.ExplainCandidates(partial)
	for _, c := range candidates {
		if strings.HasSuffix(c, ".") {
			rtx.SetCompletionOptions(rotini.CompletionOptions{NoSpace: true})
			break
		}
	}
	return candidates
}

// writeKeyInfo prints one key: the document and path, its description, then a line per fact.
func writeKeyInfo(b *strings.Builder, info codegen.KeyInfo) {
	fmt.Fprintf(b, "%s: %s\n", info.Document, info.Path)
	if info.Description != "" {
		for line := range strings.SplitSeq(info.Description, "\n") {
			if line == "" {
				b.WriteString("\n")
				continue
			}
			fmt.Fprintf(b, "  %s\n", line)
		}
	}
	fact := func(label, value string) {
		if value != "" {
			fmt.Fprintf(b, "  %-9s %s\n", label+":", value)
		}
	}
	b.WriteString("\n")
	fact("type", info.Type)
	fact("values", strings.Join(info.Enum, ", "))
	fact("default", info.Default)
	fact("examples", strings.Join(info.Examples, ", "))
	fact("pattern", info.Pattern)
	fact("hint", info.Hint)
	fact("keys", strings.Join(info.Keys, ", "))
}
