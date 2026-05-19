package rtk

// Codegen-target helpers: generated PopulateFromArgv methods on user-defined
// Inputs structs call into these helpers to project a [Result] into typed
// fields. The helpers are intentionally permissive — when a key is missing
// or a stored value doesn't match the requested type, the target is left
// untouched (handler code sees the zero value). This matches the [Get]
// semantic in registry.go and keeps codegen-emitted call sites safe by
// construction.

// AssignFlag writes the most recent scalar value bound under name in scope
// into *target. If name is absent, or the stored value does not satisfy T,
// the target is left untouched.
//
// Scope values produced by the parser are wrapped in `[]any` to preserve
// repeated-flag semantics (see [appendFlagValue]); AssignFlag picks the
// last element — what a non-slice handler expects.
func AssignFlag[T any](scope map[string]any, name string, target *T) {
	v, ok := scope[name]
	if !ok {
		return
	}
	sl, ok := v.([]any)
	if !ok || len(sl) == 0 {
		return
	}
	if typed, ok := sl[len(sl)-1].(T); ok {
		*target = typed
	}
}

// AssignFlagPtr writes a pointer to the most recent value bound under name
// in scope into *target. Useful for "Nullable" flags where the handler
// needs to distinguish "not set" (nil) from "set to zero value"
// (non-nil pointer to zero).
//
// If name is absent or the stored value does not satisfy T, *target is left
// at nil.
func AssignFlagPtr[T any](scope map[string]any, name string, target **T) {
	v, ok := scope[name]
	if !ok {
		return
	}
	sl, ok := v.([]any)
	if !ok || len(sl) == 0 {
		return
	}
	if typed, ok := sl[len(sl)-1].(T); ok {
		*target = &typed
	}
}

// AssignFlagSlice appends every value bound under name in scope into
// *target. Useful for repeated flags (`-x a -x b -x c`).
//
// If name is absent or the stored value is not a `[]any`, *target is left
// untouched. Elements that don't satisfy T are silently skipped.
func AssignFlagSlice[T any](scope map[string]any, name string, target *[]T) {
	v, ok := scope[name]
	if !ok {
		return
	}
	sl, ok := v.([]any)
	if !ok {
		return
	}
	for _, it := range sl {
		if typed, ok := it.(T); ok {
			*target = append(*target, typed)
		}
	}
}

// AssignFlagMap assigns the map bound under name in scope into *target.
//
// The parser stores map values directly (not wrapped in []any), so the
// type-assert is against M itself. Successive AssignFlagMap calls for the
// same key would just overwrite, but the parser merges incoming maps in
// [appendFlagValue] before storing — so each call to AssignFlagMap sees
// the fully merged map.
//
// If name is absent or the stored value does not satisfy M, *target is
// left untouched.
func AssignFlagMap[M ~map[K]V, K comparable, V any](scope map[string]any, name string, target *M) {
	v, ok := scope[name]
	if !ok {
		return
	}
	if m, ok := v.(M); ok {
		*target = m
	}
}

// AssignStringArg writes args[idx] to *target when idx is in range. Used
// for non-variadic positional string arguments.
func AssignStringArg(args []string, idx int, target *string) {
	if idx < len(args) {
		*target = args[idx]
	}
}

// AssignVariadicStringArg writes args[idx:] to *target. Used for the
// variadic positional []string argument (when present, must be the
// last-declared argument).
func AssignVariadicStringArg(args []string, idx int, target *[]string) {
	if idx < len(args) {
		*target = args[idx:]
	}
}

// AssignCoercedArg coerces args[idx] to T via coerce and assigns to
// *target on success. Used for typed (non-string) positional arguments —
// the parser stores arguments as raw strings in [Result.ParsedArgs] and
// the codegen-emitted PopulateFromArgv calls AssignCoercedArg to convert
// at the call site.
//
// Coercion failures are silently ignored: the target is left at its zero
// value. (Validation errors that should fail parsing are surfaced earlier
// by [validateArgumentConstraints]; AssignCoercedArg only runs after a
// successful Parse, so any remaining coercion mismatch here is a codegen
// bug, not a user input error.)
func AssignCoercedArg[T any](args []string, idx int, typeName string, target *T, coerce CoerceValueFn) {
	if idx >= len(args) {
		return
	}
	parsed, _, err := coerce(typeName, args[idx])
	if err != nil {
		return
	}
	if v, ok := parsed.(T); ok {
		*target = v
	}
}
