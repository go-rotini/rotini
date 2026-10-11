package rotini

// Definition returns the command tree the program runs, for tooling and tests that work from
// it, such as [ArgvOf]. Its slices are the program's own and shared by every run, so treat them
// as read-only.
func (p *Program) Definition() Definition {
	return p.def
}
