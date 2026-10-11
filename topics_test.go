package rotini

import (
	"slices"
	"testing"
)

// TestComplete_helpTopics pins that `help <TAB>` offers the root's help topics with its
// commands, each with its summary, and that a topic is never offered below a command.
func TestComplete_helpTopics(t *testing.T) {
	def := helpDef()
	def.Topics = []TopicDef{{Name: "environment", Summary: "the variables app reads"}, {Name: "filters"}}
	if got, want := complete(def, []string{"help", ""}, nil, nil), []string{"child", "environment\tthe variables app reads", "filters", "help\tprint help", "r\tmanage remotes", "remote\tmanage remotes"}; !slices.Equal(got, want) {
		t.Errorf("help '' = %q, want %q", got, want)
	}
	if got, want := complete(def, []string{"help", "env"}, nil, nil), []string{"environment\tthe variables app reads"}; !slices.Equal(got, want) {
		t.Errorf("help env = %q, want %q", got, want)
	}
	if got, want := complete(def, []string{"help", "remote", ""}, nil, nil), []string{"add\tadd one", "rm"}; !slices.Equal(got, want) {
		t.Errorf("help remote '' = %q, want %q", got, want)
	}
	if got := complete(def, []string{""}, nil, nil); slices.Contains(got, "filters") {
		t.Errorf("a topic was offered as a command: %q", got)
	}
}
