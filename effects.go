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

// RunEffects is what this run does: the invoked command's [Command.Effects] combined with the
// effects of every flag given on the command line, of the invoked command or a cascading flag
// of an ancestor. A bool flag counts only when it is set true, so --no-force adds nothing. The
// rule is the spec's:
//
//   - Kind is the highest stated: read, then write, then destructive;
//   - Idempotent is true only when every stated value says so, false when one says not;
//   - OpenWorld is true when any stated value says so, false when every one says not.
//
// What nothing states stays unset: a zero Kind or a nil field means the spec doesn't say,
// which a caller deciding what to ask first should treat as the worst case.
//
// RunEffects reads the resolved chain and the command line from rtx and never reads a file or
// stdin. When the command line doesn't parse, it returns the invoked command's effects alone.
// A nil rtx, or one with no resolved command, returns the zero Effects.
func RunEffects(rtx *Context) Effects {
	if rtx == nil {
		return Effects{}
	}
	chain := rtx.chainRef()
	if len(chain) == 0 {
		return Effects{}
	}
	var out effectsSum
	out.add(chain[len(chain)-1].Effects)
	store := quietParse(chain, rtx.Argv)
	if store == nil {
		return out.Effects
	}
	for i, frame := range chain {
		for j := range frame.Flags {
			fd := &frame.Flags[j]
			if fd.Effects == nil || !store.setOnArgv(i, fd.Name) {
				continue
			}
			if vals := store.scopes[i].flags[fd.Name]; fd.Type == "bool" && (len(vals) == 0 || vals[len(vals)-1] != "true") {
				continue
			}
			out.add(fd.Effects)
		}
	}
	return out.Effects
}

// effectsSum combines stated effects by the rule [RunEffects] documents.
type effectsSum struct {
	Effects

	notIdempotent, idempotent, openWorld bool
}

func (s *effectsSum) add(e *Effects) {
	if e == nil {
		return
	}
	if effectsRank(e.Kind) > effectsRank(s.Kind) {
		s.Kind = e.Kind
	}
	if e.Idempotent != nil {
		if *e.Idempotent {
			s.idempotent = true
		} else {
			s.notIdempotent = true
		}
		s.Idempotent = new(s.idempotent && !s.notIdempotent)
	}
	if e.OpenWorld != nil {
		s.openWorld = s.openWorld || *e.OpenWorld
		s.OpenWorld = new(s.openWorld)
	}
}

// effectsRank orders effect kinds: unstated, read, write, destructive. A kind rotini doesn't
// know ranks above destructive, so it is never hidden by a lower one.
func effectsRank(kind string) int {
	switch kind {
	case "":
		return 0
	case "read":
		return 1
	case "write":
		return 2
	case "destructive":
		return 3
	}
	return 4
}
