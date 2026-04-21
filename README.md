# errs

A lightweight error package providing stack traces, structured Sentry context,
and fingerprinting for use with a boundary-based error reporting strategy.

The package has no external dependencies. Sentry integration lives in your
reporting layer — this package only defines the interfaces and embeddable types
that errors use to contribute data to it.

---

## Concepts

### Boundary-based reporting

Errors are created and wrapped through your call stack as normal Go errors.
Reporting to Sentry happens once, at a _boundary_ — the point where a unit of
work terminates: an HTTP handler, a queue consumer, a background job. The
boundary calls a `Capture` function (in your reporting layer, not this package)
which walks the error chain, collects context and fingerprint data, and sends a
single event.

This means your domain and service layers have no knowledge of Sentry. They
only import `errs`.

### Embeddable types

`Base` is the primary embeddable for typed error structs. It combines stack
capture, fingerprinting, and context into one type so constructors stay concise.

The three underlying types are also available individually if you need finer
control, but most typed errors should embed `Base` directly.

| Type | Provides | Set via |
|---|---|---|
| `Base` | Stack, fingerprint, and context | `NewBase(...)` + `.Wrap` / `.New` |
| `ErrStack` | Stack trace only | `CaptureStack(1)` in `New*` |
| `ErrFingerprint` | Stable grouping key only | `NewErrFingerprint(...)` in `New*` |
| `ErrContext` | Structured context only | `AddContext` / domain helpers |

`Base` satisfies all three reporting interfaces (`StackTracer`, `Fingerprinter`,
`Contexter`). The individual embeddables each satisfy their own interface.

---

## Defining an error type

Embed `Base` and call `NewBase` in the constructor, passing the underlying
cause. The stack is captured inside `Wrap`, excluding `Wrap` itself from the
trace — the first frame recorded is the constructor, and above it the call site.

```go
type UserLoginError struct {
    errs.Base
}

func NewUserLoginError(cause error) UserLoginError {
    return UserLoginError{Base: errs.NewBase("user_login").Wrap(cause)}
}

func (e UserLoginError) Error() string {
    return "user login error"
}
```

If the error does not wrap a cause, use `New` instead of `Wrap`:

```go
func NewUserLoginError() UserLoginError {
    return UserLoginError{Base: errs.NewBase("user_login").New("user login error")}
}
```

For high-frequency error paths, define a package-level template so `NewBase`
is only called once:

```go
var errUserLogin = errs.NewBase("user_login")

func NewUserLoginError(cause error) UserLoginError {
    return UserLoginError{Base: errUserLogin.Wrap(cause)}
}
```

---

## Adding context

Add domain-specific helper methods rather than calling `AddContext` from
outside the type. This keeps context keys consistent and documents what data
the error carries as part of its API.

```go
func (e UserLoginError) ForUser(id string) UserLoginError {
    e.AddContext("user", map[string]any{"id": id})
    return e
}

func (e UserLoginError) ForRequest(ip, method string) UserLoginError {
    e.AddContext("request", map[string]any{"ip": ip, "method": method})
    return e
}
```

Because the helpers return the typed value, they chain naturally off the
constructor:

```go
return NewUserLoginError(err).ForUser(userID).ForRequest(ip, method)
```

Context values appear as named sections in the event UI of your reporting tool.
Keys within a section should be flat — avoid nesting.

---

## Fingerprinting

`ErrFingerprint` holds a fixed slice of segments set at construction. Segments
are accumulated across the unwrap chain by the reporting layer, so each error
type in a chain contributes to the final fingerprint independently.

Segments must be **stable identifiers** for the error class. Dynamic instance
data (user IDs, file paths, request parameters) belongs in `ErrContext`, not
the fingerprint — dynamic fingerprints cause every occurrence to create a
separate issue in your reporting tool.

```go
// good — stable class identifier
errs.NewErrFingerprint("user_login")

// good — stable with an error code
errs.NewErrFingerprint("upstream_api", "payment_service", "timeout")

// bad — instance data in the fingerprint
errs.NewErrFingerprint("user_login", userID)
```

---

## Wrapping external errors

Use `errs.Wrap` when receiving errors from third-party or stdlib code. It
attaches a stack trace at the call site and an optional message, and the
resulting error unwraps to the original.

```go
row, err := db.QueryContext(ctx, query)
if err != nil {
    return errs.Wrap(err, "querying users")
}
```

Use `errs.New` as a drop-in for `errors.New` when you want a stack trace on a
root error without a dedicated type.

```go
if len(items) == 0 {
    return errs.New("no items found")
}
```

---

## The reporting layer

Your reporting package consumes the three interfaces to build a Sentry event.
It is the only place in your codebase that imports `sentry-go`.

A minimal implementation:

```go
func Capture(ctx context.Context, err error) {
    sentry.WithScope(func(scope *sentry.Scope) {
        attachRequestIDs(ctx, scope)
        applyContext(scope, err)
        applyFingerprint(scope, err)

        event := sentry.NewEvent()
        event.Exception = []sentry.Exception{{
            Type:       fmt.Sprintf("%T", err),
            Value:      err.Error(),
            Stacktrace: buildStacktrace(err),
        }}

        sentry.CaptureEvent(event)
    })
}

func applyContext(scope *sentry.Scope, err error) {
    for e := err; e != nil; e = errors.Unwrap(e) {
        if sc, ok := e.(errs.Contexter); ok {
            for key, ctx := range sc.ErrorContext() {
                scope.SetContext(key, ctx)
            }
        }
    }
}

func applyFingerprint(scope *sentry.Scope, err error) {
    var fingerprint []string
    for e := err; e != nil; e = errors.Unwrap(e) {
        if sf, ok := e.(errs.Fingerprinter); ok {
            fingerprint = append(fingerprint, sf.Fingerprint()...)
        }
    }
    if len(fingerprint) > 0 {
        scope.SetFingerprint(fingerprint)
    }
}

func buildStacktrace(err error) *sentry.Stacktrace {
    for e := err; e != nil; e = errors.Unwrap(e) {
        if st, ok := e.(errs.StackTracer); ok {
            pcs := st.StackTrace()
            frames := make([]sentry.Frame, 0, len(pcs))
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
    }
    return nil
}
```

Call `Capture` at each service boundary — HTTP middleware, queue consumer loop,
background job wrapper — not in the middle of business logic.

---

## Errors without a dedicated type

Not every error warrants a named type. Use `errs.Wrap` for errors crossing
package boundaries, and `errs.New` for simple root errors. Both capture a stack
trace. Neither contributes context or fingerprint data unless you add it — the
reporting layer will fall back to Sentry's default grouping in that case.
