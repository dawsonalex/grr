package errs

import "errors"

// Def is a reusable error factory identified by pointer identity. Create one
// with Define and call New or Wrap to produce errors tied to this definition.
// Use Is to test whether an error in a chain originated from this Def.
//
//	var ErrNotFound = errs.Define("User Not Found")
//
//	// later:
//	if ErrNotFound.Is(err) { ... }
type Def struct {
	name string
}

// Define creates a new Def with the given name. The name is used as the
// fingerprint for error grouping and as the human-readable error class label.
// It should be stable — renaming a Def changes its identity and fingerprint.
func Define(name string) *Def {
	return &Def{name: name}
}

// New returns a Base error with msg and a stack trace captured at the call site.
func (d *Def) New(msg string) Base {
	b := Base{ErrFingerprint: NewErrFingerprint(d.name), msg: msg, def: d}
	b.ErrStack = CaptureStack(1)
	return b
}

// Wrap returns a Base error wrapping cause with a stack trace captured at the
// call site.
func (d *Def) Wrap(cause error) Base {
	b := Base{ErrFingerprint: NewErrFingerprint(d.name), cause: cause, def: d}
	b.ErrStack = CaptureStack(1)
	return b
}

// Is reports whether any error in err's chain was created by this Def.
// Two Define calls with identical names are treated as distinct error
// classes — identity is pointer-based, not name-based.
func (d *Def) Is(err error) bool {
	for err != nil {
		if b, ok := err.(Base); ok && b.def == d {
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}
