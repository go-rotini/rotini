package codegen

// composedTopicsProblem warns when a spec mounted with `$ref` declares help topics: only the
// program's root spec contributes topics, so the program's `help <topic>` can't reach these.
// The warning is placed at the child's topics, in the child's file.
func composedTopicsProblem(parent, child *reconciledSpec) []error {
	if child == nil || child.spec == nil || len(child.spec.Command.Topics) == 0 {
		return nil
	}
	p := &problem{kind: "spec", ptr: rootPointer + "/topics", loc: rootLabel(child.spec),
		msg: "declares `topics`, which `help <topic>` can't reach: this spec is mounted with `$ref` from " +
			parent.path + ", and only the root spec's topics are part of the program; declare them there",
		sev: severityWarning}
	locateProblems([]error{p}, child.path, child.locate)
	return []error{p}
}
