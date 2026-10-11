package rotini

// argSpans returns where each of defs binds among n positional values: span i is the
// [start, end) range of argument i, and an empty range means the argument got none.
//
// Arguments fill in declaration order, and a variadic takes what the fixed arguments around it
// leave: the arguments before it fill first, the ones after it (the `<src...> <dst>` shape)
// take the last values, and the variadic gets the values in between. With too few values the
// leading arguments still fill first, the trailing ones then fill from the end, and whatever
// is left empty is reported as missing by the required check.
func argSpans(defs []ArgDef, n int) [][2]int {
	spans := make([][2]int, len(defs))
	v := variadicIndex(defs)
	if v < 0 {
		for i := range defs {
			spans[i] = [2]int{min(i, n), min(i+1, n)}
		}
		return spans
	}
	for i := range v {
		spans[i] = [2]int{min(i, n), min(i+1, n)}
	}
	lead := min(v, n)
	tail := len(defs) - v - 1
	for j := range tail {
		at := n - (tail - j) // the tail binds from the end
		if at < lead {
			spans[v+1+j] = [2]int{lead, lead}
			continue
		}
		spans[v+1+j] = [2]int{at, at + 1}
	}
	spans[v] = [2]int{lead, max(lead, n-tail)}
	return spans
}

// variadicIndex returns the index of defs' variadic argument, or -1 when there is none.
func variadicIndex(defs []ArgDef) int {
	for i, a := range defs {
		if a.Variadic {
			return i
		}
	}
	return -1
}

// hasArgTail reports whether a variadic argument is followed by fixed ones.
func hasArgTail(defs []ArgDef) bool {
	v := variadicIndex(defs)
	return v >= 0 && v < len(defs)-1
}
