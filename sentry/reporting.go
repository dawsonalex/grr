// Package sentry provides a Sentry reporting layer for the errs package.
// It is kept separate from the core errs package so that services which do
// not use Sentry do not incur the dependency.
//
// Usage at a service boundary:
//
//	eventID := sentry.Report(ctx, err)
//	logger.Error("request failed",
//	    slog.String("error", err.Error()),
//	    slog.String("event_id", eventID),
//	)
package sentry

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"

	"github.com/dawsonalex/grr"
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

// Report captures err as a Sentry event and returns the event ID. The caller
// should include the event ID in any log output so that log lines and Sentry
// events can be correlated.
//
// Report walks the full error chain and:
//   - attaches correlation/request IDs from ctx as searchable tags
//   - attaches structured context blocks from errors implementing errs.Contexter
//   - sets the fingerprint from errors implementing errs.Fingerprinter, falling
//     back to Sentry's default grouping if none is found
//   - builds a chained exception list from errors implementing errs.StackTracer,
//     with each layer appearing as a separate exception in the Sentry UI
//   - falls back gracefully for plain errors, capturing type and message with
//     no stack trace
//
// Returns an empty string if err is nil or the event was not sent.
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

// attachErrorContext walks the error chain and applies structured context
// blocks from any error implementing errs.Contexter. Each block appears as
// a named section in the Sentry event UI. Outer layers in the chain can
// overwrite keys set by inner layers.
func attachErrorContext(scope *sentry.Scope, err error) {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if c, ok := e.(errs.Contexter); ok {
			for key, val := range c.ErrorContext() {
				scope.SetContext(key, val)
			}
		}
	}
}

// attachFingerprint walks the error chain and accumulates fingerprint segments
// from any error implementing errs.Fingerprinter. Segments are concatenated
// across layers so each layer contributes to the final grouping key
// independently. Falls back to Sentry's default grouping if no segments
// are found.
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

// attachErrorChainTag sets an "error_chain" tag showing the type of each layer
// in the unwrap chain. Useful for searching and filtering in Sentry when the
// same root cause is wrapped by different error types across call paths.
// Errors created via Define show their defined name; others show their Go type.
//
// Example value: "User Not Found → *pgconn.PgError"
func attachErrorChainTag(scope *sentry.Scope, err error) {
	var parts []string
	for e := err; e != nil; e = errors.Unwrap(e) {
		parts = append(parts, errorTypeName(e))
	}
	if len(parts) > 0 {
		scope.SetTag("error_chain", strings.Join(parts, " → "))
	}
}

// buildExceptions walks the error chain and constructs a Sentry exception for
// each layer that implements errs.StackTracer. If no layer has a stack trace
// (e.g. a plain stdlib error), a single exception is returned with no
// stacktrace so the error is still captured.
//
// Sentry renders exceptions innermost-first, so the slice is reversed before
// returning.
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
		// Plain error with no stack trace — still capture type and message.
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
// Go type name (via %T) for everything else.
func errorTypeName(err error) string {
	type typeNamer interface{ TypeName() string }
	if tn, ok := err.(typeNamer); ok {
		if name := tn.TypeName(); name != "" {
			return name
		}
	}
	return fmt.Sprintf("%T", err)
}

// buildStacktrace converts a slice of program counters (as returned by
// errs.StackTracer) into a Sentry stacktrace. Frames are reversed so the
// innermost call appears at the top of the trace in the Sentry UI.
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
