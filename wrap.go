package errs

// Wrap attaches a message and stack trace to err, returning a new error that
// unwraps to err. Use for errors crossing package boundaries that do not
// warrant a dedicated typed error. Returns nil if err is nil.
func Wrap(err error, msg string) Base {
	if err == nil {
		return New(msg)
	}
	b := Base{msg: msg}
	b.ErrStack = CaptureStack(1)
	b.cause = err
	return b
}

// New creates an error with a message and stack trace captured at the call site.
func New(msg string) Base {
	b := Base{msg: msg}
	b.ErrStack = CaptureStack(1)
	return b
}
