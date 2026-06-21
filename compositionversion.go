package rotini

// CompositionVersionArm identifies which composition surface a rotini-version mismatch
// was found on — the three places a composed command tree can disagree about the rotini
// version it targets. Each arm enforces its own policy (see [CompositionVersionError]).
type CompositionVersionArm string

const (
	// CompositionSpecArm — a composed `$ref` spec's `$schema` version (checked at
	// generate/validate; policy: EXACT match with the generating rotini).
	CompositionSpecArm CompositionVersionArm = "spec"
	// CompositionPackageArm — the rotini LIBRARY a passthrough/consuming module builds
	// against vs. the rotini TOOL that generated it (checked at generate; policy:
	// same-major). Go's MVS already resolves one library version per build.
	CompositionPackageArm CompositionVersionArm = "package"
	// CompositionBinaryArm — a dispatched remote/plugin binary's rotini version vs. the
	// host's (checked at runtime via the opt-in `__rotini` handshake; policy: same-major).
	CompositionBinaryArm CompositionVersionArm = "binary"
)

// CompositionVersionError reports a rotini-version incompatibility discovered while
// composing a command tree — one error type across all three arms (spec, package,
// binary; see [CompositionVersionArm]). It carries the generating/host version ([Want])
// and the version the subject declares or reports ([Got]), so a consumer can branch on
// [CompositionVersionError.Arm] and render its own message without parsing [Error].
//
// It unwraps to [ErrInternal] — a version mismatch is a build/wiring concern, not the
// end-user's input — so [CategoryOf] classifies it [CategoryInternal]. Recover it with
// errors.As:
//
//	var ve *rotini.CompositionVersionError
//	if errors.As(err, &ve) && ve.Arm == rotini.CompositionBinaryArm { /* … */ }
type CompositionVersionError struct {
	Arm     CompositionVersionArm // which surface disagreed
	Subject string                // the composed `$ref`, import path, or binary the mismatch is about
	Want    string                // the generating/host rotini version, "X.Y.Z"
	Got     string                // the version the subject declares/reports, "X.Y.Z" ("" = absent)
	Msg     string                // the human-readable failure
}

func (e *CompositionVersionError) Error() string { return e.Msg }

// Unwrap exposes [ErrInternal] so errors.Is matches it and [CategoryOf] reports
// [CategoryInternal] — a composition version mismatch is rotini's "this build can't be
// trusted to compose", never the end-user's fault.
func (e *CompositionVersionError) Unwrap() error { return ErrInternal }
