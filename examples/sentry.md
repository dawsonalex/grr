# Reporting to Sentry

`errs` has no dependency on Sentry. Instead, errors contribute data through
small interfaces, and a reporting layer in your service reads those interfaces
and builds the event. This page shows one way to write that layer for
[sentry-go](https://github.com/getsentry/sentry-go).

Each interface maps onto part of a Sentry event:

| Interface | Implemented by | Becomes |
|---|---|---|
| `errs.StackTracer` | `Base`, `ErrStack` | A chained exception, one per layer, each with its stack trace |
| `errs.Fingerprinter` | `Base`, `ErrFingerprint` | The event fingerprint (grouping key) |
| `errs.Contexter` | `Base`, `ErrContext` | Named context blocks in the event UI |
| `TypeName() string` | `Base` created via a `Def` | The exception type and the `error_chain` tag |

Plain errors from the standard library or third-party packages still get
reported: they appear in the `error_chain` tag, and if nothing in the chain has a
stack trace, the event falls back to a single exception with the type and
message.

## Usage

Report once, at a boundary: the HTTP handler, queue consumer or job runner where
a unit of work ends. Log the returned event ID next to the error so log lines and
Sentry events can be matched up.

```go
ctx = reporting.WithRequestID(ctx, r.Header.Get("X-Request-ID"))

if err := svc.Handle(ctx, req); err != nil {
	eventID := reporting.Report(ctx, err)
	logger.Error("request failed",
		slog.String("error", err.Error()),
		slog.String("event_id", eventID),
	)
}
```

## The reporting layer

Copy this into your service (it assumes `sentry.Init` has already been called).
The Sentry import is aliased explicitly to keep it distinct from your own
package names.

```go
// Package reporting sends errors built with errs to Sentry.
package reporting

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"

	errs "github.com/dawsonalex/grr"
	sentry "github.com/getsentry/sentry-go"
)

// ctxKey is the unexported type for context keys set by this package,
// preventing collisions with keys from other packages.
type ctxKey int

const (
	correlationIDKey ctxKey = iota
	requestIDKey
)

// WithCorrelationID returns a copy of ctx carrying the given correlation ID.
// The ID will be attached as a tag on any Sentry event reported from that ctx.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationIDKey, id)
}

// WithRequestID returns a copy of ctx carrying the given request ID.
// The ID will be attached as a tag on any Sentry event reported from that ctx.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// Report captures err as a Sentry event and returns the event ID. It returns
// an empty string if err is nil or the event was not sent.
func Report(ctx context.Context, err error) string {
	if err == nil {
		return ""
	}

	var eventID string

	sentry.WithScope(func(scope *sentry.Scope) {
		attachRequestTags(ctx, scope)
		attachErrorContext(scope, err)
		attachFingerprint(scope, err)
		attachErrorChainTag(scope, err)

		event := sentry.NewEvent()
		event.Exception = buildExceptions(err)

		if id := sentry.CaptureEvent(event); id != nil {
			eventID = string(*id)
		}
	})

	return eventID
}

// attachRequestTags reads request-scoped IDs from ctx and sets them as
// searchable Sentry tags.
func attachRequestTags(ctx context.Context, scope *sentry.Scope) {
	if id, ok := ctx.Value(correlationIDKey).(string); ok && id != "" {
		scope.SetTag("correlation_id", id)
	}
	if id, ok := ctx.Value(requestIDKey).(string); ok && id != "" {
		scope.SetTag("request_id", id)
	}
}

// attachErrorContext applies the context blocks from every layer implementing
// errs.Contexter. Outer layers overwrite keys set by inner layers.
func attachErrorContext(scope *sentry.Scope, err error) {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if c, ok := e.(errs.Contexter); ok {
			for key, val := range c.ErrorContext() {
				scope.SetContext(key, val)
			}
		}
	}
}

// attachFingerprint concatenates the fingerprint segments from every layer
// implementing errs.Fingerprinter. With no segments, Sentry's default grouping
// is used.
func attachFingerprint(scope *sentry.Scope, err error) {
	var segments []string
	for e := err; e != nil; e = errors.Unwrap(e) {
		if f, ok := e.(errs.Fingerprinter); ok {
			segments = append(segments, f.Fingerprint()...)
		}
	}
	if len(segments) > 0 {
		scope.SetFingerprint(segments)
	}
}

// attachErrorChainTag sets an "error_chain" tag naming each layer of the
// chain, e.g. "User Not Found → *pgconn.PgError". This makes it possible to
// filter on a root cause that different call paths wrap differently.
func attachErrorChainTag(scope *sentry.Scope, err error) {
	var parts []string
	for e := err; e != nil; e = errors.Unwrap(e) {
		parts = append(parts, errorTypeName(e))
	}
	if len(parts) > 0 {
		scope.SetTag("error_chain", strings.Join(parts, " → "))
	}
}

// buildExceptions builds one Sentry exception per layer implementing
// errs.StackTracer, falling back to a single exception without a stack trace
// when no layer has one.
func buildExceptions(err error) []sentry.Exception {
	var exceptions []sentry.Exception

	for e := err; e != nil; e = errors.Unwrap(e) {
		if st, ok := e.(errs.StackTracer); ok {
			exceptions = append(exceptions, sentry.Exception{
				Type:       errorTypeName(e),
				Value:      e.Error(),
				Stacktrace: buildStacktrace(st.StackTrace()),
			})
		}
	}

	if len(exceptions) == 0 {
		return []sentry.Exception{{
			Type:  errorTypeName(err),
			Value: err.Error(),
		}}
	}

	// Sentry renders exception chains innermost-first.
	slices.Reverse(exceptions)
	return exceptions
}

// errorTypeName returns the Def name for errors created via Define, and the
// Go type name for everything else.
func errorTypeName(err error) string {
	type typeNamer interface{ TypeName() string }
	if tn, ok := err.(typeNamer); ok {
		if name := tn.TypeName(); name != "" {
			return name
		}
	}
	return fmt.Sprintf("%T", err)
}

// buildStacktrace converts program counters from errs.StackTracer into a
// Sentry stacktrace, ordered so the innermost call is at the top.
func buildStacktrace(pcs []uintptr) *sentry.Stacktrace {
	if len(pcs) == 0 {
		return nil
	}

	var frames []sentry.Frame
	iter := runtime.CallersFrames(pcs)
	for {
		f, more := iter.Next()
		frames = append(frames, sentry.Frame{
			Function: f.Function,
			AbsPath:  f.File,
			Lineno:   f.Line,
		})
		if !more {
			break
		}
	}

	slices.Reverse(frames)
	return &sentry.Stacktrace{Frames: frames}
}
```

## Limitations

The chain is walked with `errors.Unwrap`, which follows a single cause. Errors
that wrap several causes (`errors.Join`, or `fmt.Errorf` with more than one
`%w`) stop the walk at that layer. If you rely on those, replace the loops with a
traversal that also checks for `Unwrap() []error`.
