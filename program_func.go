package rotini

import "reflect"

// HandlerLookup returns the [Handler] for one command, given the handler name its
// [CommandDef.Handler] (or [Definition.Handler]) holds. It reports false for a name it does
// not know. It is called once per command in the chain on every run, so it should return a
// new handler per call: a handler's fields are per-run state.
type HandlerLookup func(name string) (Handler, bool)

// NewProgramFunc wires a command tree to the runtime through lookup, with no reflection. It is
// what generated code calls: the generated NewProgram passes a switch over the ProgramHandlers
// methods. Hand-written handler sets can use it the same way for a smaller binary than
// [NewProgram] gives.
//
// A nil lookup is accepted here; a run that reaches dispatch then fails with a [*WiringError],
// and completion offers no dynamic candidates.
func NewProgramFunc(def Definition, lookup HandlerLookup) *Program {
	p := newProgram(def, lookup)
	if lookup == nil {
		p.noHandlers = "no handlers: NewProgramFunc was given a nil lookup"
	}
	return p
}

// reflectLookup finds each handler by calling the method of handlers with the command's
// handler name. It is reachable only from [NewProgram], so a program built with
// [NewProgramFunc] links no reflective method lookup. A nil handlers value gives a nil
// lookup.
func reflectLookup(handlers any) HandlerLookup {
	hv := reflect.ValueOf(handlers)
	if !hv.IsValid() {
		return nil
	}
	return func(name string) (Handler, bool) {
		m := hv.MethodByName(name)
		if !m.IsValid() || m.Type().NumIn() != 0 || m.Type().NumOut() != 1 {
			return nil, false
		}
		h, _ := reflect.TypeAssert[Handler](m.Call(nil)[0])
		return h, true
	}
}
