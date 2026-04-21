package errs

import "runtime"

// StackTracer is implemented by errors that carry a captured stack trace.
// The returned values are program counters compatible with runtime.CallersFrames.
type StackTracer interface {
	StackTrace() []uintptr
}

// ErrStack captures the call stack at the point it is created.
// Embed it in error types and initialise with CaptureStack.
type ErrStack struct {
	pcs []uintptr
}

func (s ErrStack) StackTrace() []uintptr {
	return s.pcs
}

// CaptureStack records the current call stack. skip is the number of additional
// frames to omit above CaptureStack itself — pass 1 from a New* constructor so
// the constructor frame is excluded and the trace starts at the call site.
func CaptureStack(skip int) ErrStack {
	var pcs [64]uintptr
	n := runtime.Callers(skip+2, pcs[:])
	return ErrStack{pcs: pcs[:n]}
}
