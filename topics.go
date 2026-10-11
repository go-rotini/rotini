package rotini

// TopicDef is one help topic the root declares (the spec's `topics:`): a page that isn't a
// command, shown by the generated Help function for its name. Completion offers topic names
// with the root's sub-commands when it completes a command path (`help <TAB>`).
type TopicDef struct {
	Name    string
	Summary string
}

// topicCandidates returns the root's help topics as completion candidates, each with its
// summary.
func topicCandidates(def Definition) []string {
	out := make([]string, 0, len(def.Topics))
	for _, t := range def.Topics {
		out = append(out, withDescription(t.Name, t.Summary))
	}
	return out
}
