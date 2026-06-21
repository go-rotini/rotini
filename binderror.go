package rotini

import (
	"errors"
	"fmt"
	"strings"

	"github.com/go-rotini/recon"
)

// The non-argv input channels a [BindError] can report on. They name the
// surface an end-user (or author) reasons about, not recon's internals.
const (
	channelEnv    = "env"
	channelConfig = "config"
	channelStdin  = "stdin"
	channelFlag   = "flag"
)

// BindError reports a failure acquiring or decoding one of a command's non-argv
// input channels — environment variables, configuration files, a typed stdin
// payload, or a flag's env/config fallback. It is the bind channels' answer to
// the argv channel's [*ParseError]: a typed, categorized, NON-LEAKY error a
// funnel can branch on.
//
// Error renders a clean, rotini-owned message (Channel + Input + what went
// wrong); the underlying recon/decode/OS Cause stays reachable via errors.As /
// [errors.Unwrap] but is deliberately kept OUT of the message, so raw recon
// text ("recon: coerce no_styles: string → bool") never reaches a terminal.
// Values are never echoed — a secret can't leak through a BindError's message.
//
// It is categorized through the reused [ErrUsage] / [ErrInternal] sentinels (no
// finer sentinels — the type itself is the structured handle): a bad value, a
// missing-required input, or a malformed document the user supplied is
// [CategoryUsage]; a registry build, a schema compile, an IO read, or a
// codegen/spec mismatch is [CategoryInternal]. Recover the structure with
// errors.As, classify with [CategoryOf] (or errors.Is against the sentinels):
//
//	var be *rotini.BindError
//	if errors.As(err, &be) {
//	    fmt.Fprintf(os.Stderr, "bad %s input %q: %s\n", be.Channel, be.Input, be.Error())
//	}
type BindError struct {
	Channel string // one of "env", "config", "stdin", "flag"
	Input   string // the offending input key/path, when a single one is known (else "")
	Msg     string // a clean, non-leaky, rotini-owned message
	Cause   error  // the underlying recon/decode/OS error, reachable via errors.As (may be nil)

	usage bool // true → CategoryUsage (ErrUsage); false → CategoryInternal
}

func (e *BindError) Error() string { return e.Msg }

// Unwrap exposes the [Cause] (when present) and the category sentinel via
// multi-unwrap, so errors.Is/As reach both the original recon error and
// [ErrUsage]/[ErrInternal] — while [Error] stays the clean message, adding no
// recon text.
func (e *BindError) Unwrap() []error {
	sentinel := ErrInternal
	if e.usage {
		sentinel = ErrUsage
	}
	if e.Cause == nil {
		return []error{sentinel}
	}
	return []error{e.Cause, sentinel}
}

// usageBind builds a [CategoryUsage] *BindError — bad input the end-user can fix.
func usageBind(channel, input, msg string, cause error) *BindError {
	return &BindError{Channel: channel, Input: input, Msg: msg, Cause: cause, usage: true}
}

// internalBind builds a [CategoryInternal] *BindError — a registry/IO/schema/
// codegen failure the program author must fix.
func internalBind(channel, input, msg string, cause error) *BindError {
	return &BindError{Channel: channel, Input: input, Msg: msg, Cause: cause}
}

// reconBind converts a recon Bind/Get/Validate error for one channel into a
// clean, categorized [*BindError]. It inspects recon's typed errors to name the
// offending input and phrase a non-leaky message, keeping the whole error as
// the Cause so errors.As still reaches every recon field. Recognized failures
// (coercion, missing-required, validation, empty) are usage-class; an
// unrecognized recon error becomes a generic usage-class channel failure (a
// channel value the user supplied could not be used) — its detail stays
// reachable via the Cause.
func reconBind(channel string, err error) error {
	if err == nil {
		return nil
	}
	noun := channelNoun(channel)
	var ce *recon.CoercionError
	var mre *recon.MissingRequiredError
	var ve *recon.ValidationError
	var eve *recon.EmptyValueError
	switch {
	case errors.As(err, &ce):
		return usageBind(channel, ce.Path.String(),
			fmt.Sprintf("%s %q: expected %s", noun, ce.Path.String(), cleanType(ce.Target)), err)
	case errors.As(err, &mre):
		return usageBind(channel, mre.Path.String(),
			fmt.Sprintf("%s %q is required", noun, mre.Path.String()), err)
	case errors.As(err, &ve):
		return usageBind(channel, ve.Path.String(),
			fmt.Sprintf("%s %q: %s", noun, ve.Path.String(), ve.Msg), err)
	case errors.As(err, &eve):
		return usageBind(channel, eve.Path.String(),
			fmt.Sprintf("%s %q must not be empty", noun, eve.Path.String()), err)
	default:
		return usageBind(channel, "", fmt.Sprintf("could not read %s input", channelDesc(channel)), err)
	}
}

// channelNoun names a single input on a channel, for per-input messages.
func channelNoun(channel string) string {
	switch channel {
	case channelEnv:
		return "environment variable"
	case channelConfig:
		return "config key"
	case channelStdin:
		return "stdin field"
	default:
		return "flag"
	}
}

// channelDesc names the channel as a whole, for generic messages.
func channelDesc(channel string) string {
	switch channel {
	case channelEnv:
		return "environment"
	case channelConfig:
		return "configuration"
	case channelStdin:
		return "stdin"
	default:
		return "flag"
	}
}

// schemaDetail extracts a clean, non-leaky detail from a recon schema
// [recon.ValidationError] — the property/rule it names, redacted by recon when
// secret — falling back to a generic phrase when the cause is some other error,
// so the document-shape message never echoes raw recon text.
func schemaDetail(err error) string {
	if ve, ok := errors.AsType[*recon.ValidationError](err); ok {
		if ve.Path.String() != "" {
			return fmt.Sprintf("%s: %s", ve.Path.String(), ve.Msg)
		}
		return ve.Msg
	}
	return "does not match its schema"
}

// channelForStruct maps a generated channel sub-struct name ("Env"/"Config"/
// "Stdin") to its [BindError] channel label.
func channelForStruct(structName string) string {
	switch structName {
	case "Config":
		return channelConfig
	case "Stdin":
		return channelStdin
	default:
		return channelEnv
	}
}

// cleanType renders a recon target Go type for an end-user message: a nullable
// scalar's pointer star is noise here (the user supplies a value, not a
// pointer), so it is trimmed.
func cleanType(t string) string { return strings.TrimPrefix(t, "*") }
