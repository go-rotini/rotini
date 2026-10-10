package rotini

// Effects is what a command does when it runs, or what one of its flags adds to that: the
// spec's `effects`. Rotini doesn't act on it; it is data for a reporter, a wrapper or a tool
// that runs the CLI for someone and decides what needs asking first.
type Effects struct {
	// Kind is "read" (changes nothing), "write" (creates or changes things) or "destructive"
	// (deletes or overwrites what can't be got back).
	Kind string
	// Idempotent says whether running it again with the same inputs changes nothing more;
	// nil when the spec doesn't say.
	Idempotent *bool
	// OpenWorld says whether it reaches the network or other systems outside the machine;
	// nil when the spec doesn't say.
	OpenWorld *bool
}
