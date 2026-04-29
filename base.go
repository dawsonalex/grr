package errs

// Base combines ErrStack, ErrFingerprint, and ErrContext into a single
// embeddable that handles the common setup for typed error structs.
//
// Define typed errors by embedding Base and calling NewBase in the constructor.
// NewBase accepts a cause so the underlying error is captured at the call site,
// and domain-specific context methods chain off the returned typed value:
//
//	type UserLoginError struct {
//	    errs.Base
//	}
//
//	func NewUserLoginError(cause error) UserLoginError {
//	    return UserLoginError{Base: errs.NewBase("user_login").Wrap(cause)}
//	}
//
//	func (e UserLoginError) ForUser(id string) UserLoginError {
//	    e.AddContext("user", map[string]any{"id": id})
//	    return e
//	}
//
// Usage:
//
//	return NewUserLoginError(err).ForUser(userID)
//
// For errors that do not need a dedicated type, use the package-level Wrap and
// New functions instead.
type Base struct {
	ErrStack
	ErrFingerprint
	ErrContext
	cause error
	msg   string
	def   *Def
}

// NewBase returns a Base initialised with the given fingerprint segments, with
// no stack captured yet. Call Wrap or New on the result to create an instance
// with a stack trace.
//
// NewBase can be called once at package level as a reusable template:
//
//	var errUserLogin = errs.NewBase("user_login")
//
//	func NewUserLoginError(cause error) UserLoginError {
//	    return UserLoginError{Base: errUserLogin.Wrap(cause)}
//	}
func NewBase(fingerprint ...string) Base {
	return Base{
		ErrFingerprint: NewErrFingerprint(fingerprint...),
	}
}

// Wrap returns a copy of b with cause set and the call stack captured at the
// call site. The Wrap call itself is excluded from the trace.
func (b Base) Wrap(cause error) Base {
	b.ErrStack = CaptureStack(1)
	b.cause = cause
	return b
}

// New returns a copy of b with msg set and the call stack captured at the
// call site. The New call itself is excluded from the trace.
func (b Base) New(msg string) Base {
	b.ErrStack = CaptureStack(1)
	b.msg = msg
	return b
}

func (b Base) Error() string {
	switch {
	case b.msg != "" && b.cause != nil:
		return b.msg + ": " + b.cause.Error()
	case b.msg != "":
		return b.msg
	case b.cause != nil:
		return b.cause.Error()
	default:
		return "unknown error"
	}
}

func (b Base) Unwrap() error {
	return b.cause
}

// TypeName returns the name from the Def that created this error, if any.
// Returns empty string for errors not created via Define.
func (b Base) TypeName() string {
	if b.def != nil {
		return b.def.name
	}
	return ""
}

func (b Base) WithContext(key string, ctx map[string]any) Base {
	b.AddContext(key, ctx)
	return b
}

// NewSkip returns a copy of b with msg set and the stack captured skip+1 frames
// above NewSkip itself. Use inside typed constructors that wrap Base.New to
// push the captured frame up to the constructor's caller:
//
//	func NewServiceError(msg string) ServiceError {
//	    return ServiceError{errService.NewSkip(msg, 1)}
//	}
func (b Base) NewSkip(msg string, skip int) Base {
	b.ErrStack = CaptureStack(skip + 1)
	b.msg = msg
	return b
}

// WrapSkip returns a copy of b with cause set and the stack captured skip+1
// frames above WrapSkip itself. Use inside typed constructors that wrap
// Base.Wrap to push the captured frame up to the constructor's caller:
//
//	func NewServiceError(cause error) ServiceError {
//	    return ServiceError{errService.WrapSkip(cause, 1)}
//	}
func (b Base) WrapSkip(cause error, skip int) Base {
	b.ErrStack = CaptureStack(skip + 1)
	b.cause = cause
	return b
}
