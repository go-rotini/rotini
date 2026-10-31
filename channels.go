package rotini

import (
	"fmt"
	"reflect"
	"slices"
)

// ChannelClass is how rotini treats the records of a [Channel]: where the default reporter
// prints them, and whether they count as a failure.
type ChannelClass uint8

const (
	// ClassInfo records print with the infos: neutral output, no bearing on the exit code.
	ClassInfo ChannelClass = iota
	// ClassSuccess records print with the successes.
	ClassSuccess
	// ClassWarning records print with the warnings, prefixed "Warning:", and never raise the
	// exit code.
	ClassWarning
	// ClassError records are failures, as [Context.RecordError] records are: they print with
	// the errors, prefixed "Error:", and count for the default exit code, [Context.Failed] and
	// the error [Program.Run] returns. A ClassError channel's type must implement error.
	ClassError
)

// String returns the class's name: info, success, warning or error.
func (c ChannelClass) String() string {
	switch c {
	case ClassInfo:
		return "info"
	case ClassSuccess:
		return "success"
	case ClassWarning:
		return "warning"
	case ClassError:
		return "error"
	}
	return fmt.Sprintf("ChannelClass(%d)", uint8(c))
}

// Channel is a typed outcome channel of the author's own, beside the built-in infos,
// successes, warnings and errors. A hook records into it with [Context.Record]; the reporter
// reads it back, typed, with [Outcome.Records]. Declare one per kind of record, once, as a
// package-level value:
//
//	var Skipped = rotini.NewChannel[SkippedFile]("skipped", rotini.ClassInfo)
//
//	// in a hook
//	rtx.Record(Skipped, SkippedFile{Path: path, Reason: "binary"})
//
//	// in the reporter
//	for _, s := range out.Records(Skipped) { … }
//
// Like any record, recording neither prints nor stops the lifecycle. The class decides what
// rotini does with the records when the author's reporter doesn't: [ChannelClass].
type Channel[T any] struct {
	name  string
	class ChannelClass
}

// NewChannel returns the channel named name, of class class. It panics when name is empty or
// is one of the built-in outcome names (info, success, warning, error, panic), when class is
// not a declared ChannelClass, or when class is [ClassError] and T does not implement error.
// A channel is identified by its name: two channels with one name must have the same type and
// class, or recording into the second is a [*WiringError].
func NewChannel[T any](name string, class ChannelClass) Channel[T] {
	switch name {
	case "":
		panic("rotini: NewChannel: empty channel name")
	case "info", "success", "warning", "error", "panic":
		panic(fmt.Sprintf("rotini: NewChannel: %q is a built-in outcome name", name))
	}
	if class > ClassError {
		panic(fmt.Sprintf("rotini: NewChannel %q: unknown class %s", name, class))
	}
	if class == ClassError && !reflect.TypeFor[T]().Implements(reflect.TypeFor[error]()) {
		panic(fmt.Sprintf("rotini: NewChannel %q: a ClassError channel needs a type that implements error, not %s",
			name, reflect.TypeFor[T]()))
	}
	return Channel[T]{name: name, class: class}
}

// Name returns the channel's name.
func (c Channel[T]) Name() string { return c.name }

// Class returns the channel's class.
func (c Channel[T]) Class() ChannelClass { return c.class }

// String returns the channel's name.
func (c Channel[T]) String() string { return c.name }

// channelRecords holds one channel's records for a run.
type channelRecords struct {
	name   string
	class  ChannelClass
	typ    reflect.Type
	values []any
}

// Record records v into ch for the reporter, in recording order. A nil v (a nil error or other
// nil interface value) is ignored, as the built-in record methods ignore one. It is safe for
// concurrent use.
//
// Recording into a channel whose name another channel of a different type or class already
// used in this run records a [*WiringError] fault instead.
func (rtx *Context) Record[T any](ch Channel[T], v T) {
	if any(v) == nil || ch.name == "" {
		return
	}
	typ := reflect.TypeFor[T]()
	rtx.mu.Lock()
	var mismatch *channelRecords
	recorded := false
	for _, c := range rtx.channels {
		if c.name != ch.name {
			continue
		}
		if c.typ != typ || c.class != ch.class {
			mismatch = c
			break
		}
		c.values = append(c.values, v)
		recorded = true
		break
	}
	if mismatch == nil && !recorded {
		rtx.channels = append(rtx.channels, &channelRecords{name: ch.name, class: ch.class, typ: typ, values: []any{v}})
	}
	rtx.mu.Unlock()
	if mismatch != nil {
		rtx.recordFault(asFault(&WiringError{Msg: fmt.Sprintf(
			"channel %q is recorded as %s (%s) and as %s (%s); one name needs one type and class",
			ch.name, mismatch.typ, mismatch.class, typ, ch.class)}))
	}
}

// copyChannels snapshots the author channels, each with its own copy of the records.
func (rtx *Context) copyChannels() []*channelRecords {
	if rtx == nil {
		return nil
	}
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	if len(rtx.channels) == 0 {
		return nil
	}
	out := make([]*channelRecords, len(rtx.channels))
	for i, c := range rtx.channels {
		cp := *c
		cp.values = slices.Clone(c.values)
		out[i] = &cp
	}
	return out
}

// failedChannel reports whether any ClassError channel holds a record.
func failedChannel(channels []*channelRecords) bool {
	for _, c := range channels {
		if c.class == ClassError && len(c.values) > 0 {
			return true
		}
	}
	return false
}

// Records returns ch's records from the run, in recording order, typed. It is a copy: changing
// it changes nothing else. A channel nothing recorded into returns nil.
func (o Outcome) Records[T any](ch Channel[T]) []T {
	for _, c := range o.channels {
		if c.name != ch.name || c.typ != reflect.TypeFor[T]() || c.class != ch.class {
			continue
		}
		out := make([]T, 0, len(c.values))
		for _, v := range c.values {
			out = append(out, v.(T)) //nolint:forcetypeassert // the channel's type was checked on record
		}
		return out
	}
	return nil
}

// channelValues returns the records of every author channel of class, in first-recorded
// channel order and recording order within each.
func (o Outcome) channelValues(class ChannelClass) []any {
	var out []any
	for _, c := range o.channels {
		if c.class == class {
			out = append(out, c.values...)
		}
	}
	return out
}

// channelErrors returns the records of every ClassError channel, as errors.
func (o Outcome) channelErrors() []error {
	values := o.channelValues(ClassError)
	if len(values) == 0 {
		return nil
	}
	out := make([]error, 0, len(values))
	for _, v := range values {
		out = append(out, v.(error)) //nolint:forcetypeassert // NewChannel requires an error type for ClassError
	}
	return out
}
