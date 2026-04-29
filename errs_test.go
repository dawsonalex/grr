package errs

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

// Compile-time interface satisfaction checks.
var (
	_ StackTracer   = ErrStack{}
	_ Contexter     = ErrContext{}
	_ Fingerprinter = ErrFingerprint{}
	_ StackTracer   = Base{}
	_ Contexter     = Base{}
	_ Fingerprinter = Base{}
	_ error         = Base{}
)

// -- ErrStack / CaptureStack --------------------------------------------------

func TestCaptureStack_RecordsFrames(t *testing.T) {
	s := CaptureStack(0)
	if len(s.StackTrace()) == 0 {
		t.Fatal("expected non-empty stack trace")
	}
}

func TestCaptureStack_FirstFrameIsDirectCaller(t *testing.T) {
	s := CaptureStack(0)
	first := firstFrame(s.StackTrace())
	if !strings.Contains(first, "TestCaptureStack_FirstFrameIsDirectCaller") {
		t.Errorf("expected first frame to be the test function, got %q", first)
	}
}

func TestCaptureStack_SkipExcludesCallerFrames(t *testing.T) {
	// CaptureStack(1) should skip one additional frame above CaptureStack —
	// meaning the first recorded frame is the caller of the function that
	// called CaptureStack, not the function itself.
	helper := func() ErrStack { return CaptureStack(1) }
	s := helper()
	first := firstFrame(s.StackTrace())
	if !strings.Contains(first, "TestCaptureStack_SkipExcludesCallerFrames") {
		t.Errorf("expected first frame to be the test function, got %q", first)
	}
}

// -- ErrContext ---------------------------------------------------------------

func TestErrContext_ZeroValueReturnsNil(t *testing.T) {
	var c ErrContext
	if c.ErrorContext() != nil {
		t.Error("expected nil for zero value")
	}
}

func TestErrContext_AddContextSetsValue(t *testing.T) {
	var c ErrContext
	c.AddContext("user", map[string]any{"id": "123"})

	ctx := c.ErrorContext()
	if ctx == nil {
		t.Fatal("expected non-nil context after AddContext")
	}
	if ctx["user"]["id"] != "123" {
		t.Errorf("expected id=123, got %v", ctx["user"]["id"])
	}
}

func TestErrContext_AddContextAccumulates(t *testing.T) {
	var c ErrContext
	c.AddContext("user", map[string]any{"id": "123"})
	c.AddContext("request", map[string]any{"ip": "1.2.3.4"})

	ctx := c.ErrorContext()
	if ctx["user"]["id"] != "123" {
		t.Errorf("expected user.id=123, got %v", ctx["user"]["id"])
	}
	if ctx["request"]["ip"] != "1.2.3.4" {
		t.Errorf("expected request.ip=1.2.3.4, got %v", ctx["request"]["ip"])
	}
}

func TestErrContext_ValueCopiesAreIndependent(t *testing.T) {
	// Simulates the ForUser pattern: a value-receiver method copies the error
	// struct, then calls AddContext. The original must not be affected.
	var original ErrContext
	original.AddContext("key", map[string]any{"v": "original"})

	// Simulate what a value-receiver method does: copy the struct, then write.
	copied := original
	copied.AddContext("key", map[string]any{"v": "copy"})

	if got := original.ErrorContext()["key"]["v"]; got != "original" {
		t.Errorf("original was mutated: expected %q, got %q", "original", got)
	}
	if got := copied.ErrorContext()["key"]["v"]; got != "copy" {
		t.Errorf("copy has wrong value: expected %q, got %q", "copy", got)
	}
}

// -- ErrFingerprint -----------------------------------------------------------

func TestErrFingerprint_ZeroValueReturnsNil(t *testing.T) {
	var f ErrFingerprint
	if f.Fingerprint() != nil {
		t.Error("expected nil for zero value")
	}
}

func TestNewErrFingerprint_StoresSegments(t *testing.T) {
	f := NewErrFingerprint("user_login", "auth")
	got := f.Fingerprint()

	if len(got) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(got))
	}
	if got[0] != "user_login" || got[1] != "auth" {
		t.Errorf("unexpected segments: %v", got)
	}
}

func TestNewErrFingerprint_NoSegments(t *testing.T) {
	f := NewErrFingerprint()
	if len(f.Fingerprint()) != 0 {
		t.Errorf("expected empty fingerprint, got %v", f.Fingerprint())
	}
}

// -- Base ---------------------------------------------------------------------

func TestBase_WrapSetsCause(t *testing.T) {
	cause := errors.New("original")
	b := NewBase("test").Wrap(cause)

	if !errors.Is(b, cause) {
		t.Error("expected errors.Is to find cause through Base")
	}
}

func TestBase_WrapExcludesItselfFromStack(t *testing.T) {
	b := NewBase("test").Wrap(errors.New("cause"))
	assertNoFrame(t, b.StackTrace(), "errs.Base.Wrap")
}

func TestBase_WrapFirstFrameIsCaller(t *testing.T) {
	b := NewBase("test").Wrap(errors.New("cause"))
	first := firstFrame(b.StackTrace())
	if !strings.Contains(first, "TestBase_WrapFirstFrameIsCaller") {
		t.Errorf("expected first frame to be the test function, got %q", first)
	}
}

func TestBase_NewSetsMessage(t *testing.T) {
	b := NewBase("test").New("something went wrong")
	if b.Error() != "something went wrong" {
		t.Errorf("unexpected error message: %q", b.Error())
	}
}

func TestBase_NewExcludesItselfFromStack(t *testing.T) {
	b := NewBase("test").New("msg")
	assertNoFrame(t, b.StackTrace(), "errs.Base.New")
}

func TestBase_Error(t *testing.T) {
	cause := errors.New("cause")

	tests := []struct {
		name string
		base Base
		want string
	}{
		{
			name: "message only",
			base: NewBase().New("something failed"),
			want: "something failed",
		},
		{
			name: "cause only",
			base: NewBase().Wrap(cause),
			want: "cause",
		},
		{
			name: "message and cause",
			base: Base{msg: "context", cause: cause},
			want: "context: cause",
		},
		{
			name: "neither",
			base: Base{},
			want: "unknown error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.base.Error(); got != tt.want {
				t.Errorf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestBase_UnwrapReturnsCause(t *testing.T) {
	cause := errors.New("root")
	b := NewBase().Wrap(cause)

	if errors.Unwrap(b) != cause {
		t.Error("expected Unwrap to return the wrapped cause")
	}
}

func TestBase_NilCauseUnwrapsToNil(t *testing.T) {
	b := NewBase().New("msg")
	if errors.Unwrap(b) != nil {
		t.Error("expected Unwrap to return nil when no cause is set")
	}
}

// -- Package-level Wrap / New -------------------------------------------------

func TestWrap_NilFallsBackToNew(t *testing.T) {
	// Wrap with a nil cause falls back to New(msg): the result carries the
	// message and no wrapped cause.
	err := Wrap(nil, "msg")
	if err.Error() != "msg" {
		t.Errorf("expected Wrap(nil) to produce message %q, got %q", "msg", err.Error())
	}
	if errors.Unwrap(err) != nil {
		t.Error("expected Wrap(nil) to produce no wrapped cause")
	}
}

func TestWrap_WrapsError(t *testing.T) {
	cause := errors.New("root")
	err := Wrap(cause, "context")

	if !errors.Is(err, cause) {
		t.Error("expected errors.Is to find cause through Wrap")
	}
	if err.Error() != "context: root" {
		t.Errorf("unexpected error message: %q", err.Error())
	}
}

func TestWrap_ExcludesItselfFromStack(t *testing.T) {
	err := Wrap(errors.New("cause"), "msg")
	assertNoFrame(t, err.StackTrace(), "errs.Wrap")
}

func TestWrap_FirstFrameIsCaller(t *testing.T) {
	err := Wrap(errors.New("cause"), "msg")
	first := firstFrame(err.StackTrace())
	if !strings.Contains(first, "TestWrap_FirstFrameIsCaller") {
		t.Errorf("expected first frame to be the test function, got %q", first)
	}
}

func TestNew_CreatesError(t *testing.T) {
	err := New("something failed")
	if err.Error() != "something failed" {
		t.Errorf("unexpected error message: %q", err.Error())
	}
}

func TestNew_ExcludesItselfFromStack(t *testing.T) {
	err := New("msg")
	assertNoFrame(t, err.StackTrace(), "errs.New")
}

// -- Typed error integration --------------------------------------------------

func TestTypedError_FullPattern(t *testing.T) {
	// Validates the complete intended usage pattern:
	// embed Base, accept cause, chain domain helpers.
	type UserLoginError struct {
		Base
	}

	newUserLoginError := func(cause error) UserLoginError {
		return UserLoginError{Base: NewBase("user_login").Wrap(cause)}
	}

	forUser := func(e UserLoginError, id string) UserLoginError {
		e.AddContext("user", map[string]any{"id": id})
		return e
	}

	cause := errors.New("invalid credentials")
	err := forUser(newUserLoginError(cause), "user-123")

	// errors.As finds the typed error
	var target UserLoginError
	if !errors.As(err, &target) {
		t.Fatal("expected errors.As to find UserLoginError")
	}

	// errors.Is finds the root cause through the typed error
	if !errors.Is(err, cause) {
		t.Error("expected errors.Is to find cause through UserLoginError")
	}

	// Fingerprint is set
	if got := err.Fingerprint(); len(got) == 0 || got[0] != "user_login" {
		t.Errorf("unexpected fingerprint: %v", got)
	}

	// Context is set
	if err.ErrorContext()["user"]["id"] != "user-123" {
		t.Errorf("unexpected context: %v", err.ErrorContext())
	}

	// Stack trace is present and excludes Wrap
	assertNoFrame(t, err.StackTrace(), "errs.Base.Wrap")
}

func TestTypedError_ChainedHelpersAreIndependent(t *testing.T) {
	// Two errors derived from the same base must not share context state.
	type OrderError struct {
		Base
	}

	base := NewBase("order").Wrap(errors.New("cause"))

	forOrder := func(e OrderError, id string) OrderError {
		e.AddContext("order", map[string]any{"id": id})
		return e
	}

	err1 := forOrder(OrderError{Base: base}, "order-1")
	err2 := forOrder(OrderError{Base: base}, "order-2")

	if err1.ErrorContext()["order"]["id"] != "order-1" {
		t.Errorf("err1 context wrong: %v", err1.ErrorContext()["order"]["id"])
	}
	if err2.ErrorContext()["order"]["id"] != "order-2" {
		t.Errorf("err2 context wrong: %v", err2.ErrorContext()["order"]["id"])
	}
}

// -- Def ----------------------------------------------------------------------

func TestDef_IsTrueForOwnError(t *testing.T) {
	d := Define("service_error")
	err := d.New("something failed")
	if !d.Is(err) {
		t.Error("expected Def.Is to return true for an error it created")
	}
}

func TestDef_IsTrueForWrappedOwnError(t *testing.T) {
	d := Define("service_error")
	cause := errors.New("root")
	err := d.Wrap(cause)
	if !d.Is(err) {
		t.Error("expected Def.Is to return true for a Wrap error it created")
	}
}

func TestDef_IsTrueDeepInChain(t *testing.T) {
	d := Define("service_error")
	inner := d.New("inner")
	chained := Wrap(inner, "outer")
	if !d.Is(chained) {
		t.Error("expected Def.Is to return true when own error is deeper in chain")
	}
}

func TestDef_IsFalseForDifferentDef(t *testing.T) {
	d1 := Define("service_error")
	d2 := Define("service_error") // same name, different pointer
	err := d1.New("something failed")
	if d2.Is(err) {
		t.Error("expected Def.Is to return false for an error created by a different Def with the same name")
	}
}

func TestDef_IsFalseForPlainNew(t *testing.T) {
	d := Define("service_error")
	err := New("something failed")
	if d.Is(err) {
		t.Error("expected Def.Is to return false for a plain grr.New error")
	}
}

func TestDef_IsFalseForPlainNewBase(t *testing.T) {
	d := Define("service_error")
	err := NewBase("service_error").New("something failed")
	if d.Is(err) {
		t.Error("expected Def.Is to return false for an error created via NewBase (no def pointer)")
	}
}

// -- Base.NewSkip / Base.WrapSkip ---------------------------------------------

func TestBase_WrapSkip_ZeroSameasWrap(t *testing.T) {
	b := NewBase("test").WrapSkip(errors.New("cause"), 0)
	first := firstFrame(b.StackTrace())
	if !strings.Contains(first, "TestBase_WrapSkip_ZeroSameasWrap") {
		t.Errorf("expected first frame to be test function, got %q", first)
	}
}

// wrapSkipConstructor simulates a typed constructor that delegates to WrapSkip
// with skip=1 so the trace lands at the constructor's caller, not here.
func wrapSkipConstructor(cause error) Base {
	return NewBase("test").WrapSkip(cause, 1)
}

func TestBase_WrapSkip_OneSkipsConstructor(t *testing.T) {
	err := wrapSkipConstructor(errors.New("root"))
	first := firstFrame(err.StackTrace())
	if !strings.Contains(first, "TestBase_WrapSkip_OneSkipsConstructor") {
		t.Errorf("expected first frame to be the test function (constructor's caller), got %q", first)
	}
}

func newSkipConstructor(msg string) Base {
	return NewBase("test").NewSkip(msg, 1)
}

func TestBase_NewSkip_OneSkipsConstructor(t *testing.T) {
	err := newSkipConstructor("something failed")
	first := firstFrame(err.StackTrace())
	if !strings.Contains(first, "TestBase_NewSkip_OneSkipsConstructor") {
		t.Errorf("expected first frame to be the test function (constructor's caller), got %q", first)
	}
}

// -- Helpers ------------------------------------------------------------------

// firstFrame returns the function name of the first frame in pcs.
func firstFrame(pcs []uintptr) string {
	if len(pcs) == 0 {
		return ""
	}
	frames := runtime.CallersFrames(pcs)
	f, _ := frames.Next()
	return f.Function
}

// assertNoFrame fails the test if any frame in pcs has a function name
// containing the given substring.
func assertNoFrame(t *testing.T, pcs []uintptr, funcNameContains string) {
	t.Helper()
	frames := runtime.CallersFrames(pcs)
	for {
		f, more := frames.Next()
		if strings.Contains(f.Function, funcNameContains) {
			t.Errorf("expected %q to be excluded from stack trace", funcNameContains)
			return
		}
		if !more {
			break
		}
	}
}
