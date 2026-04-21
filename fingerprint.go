package errs

// Fingerprinter is implemented by errors that contribute segments to a
// fingerprint used for grouping. The reporting layer accumulates segments from
// every error in the unwrap chain so each layer can contribute independently.
type Fingerprinter interface {
	Fingerprint() []string
}

// ErrFingerprint is an embeddable type that provides a fixed fingerprint for
// an error class. Segments should be stable identifiers — never include dynamic
// instance data, which belongs in ErrContext instead.
// Initialise with NewErrFingerprint in your New* constructor.
type ErrFingerprint struct {
	segments []string
}

// NewErrFingerprint constructs an ErrFingerprint with the given segments.
func NewErrFingerprint(segments ...string) ErrFingerprint {
	return ErrFingerprint{segments: segments}
}

func (f ErrFingerprint) Fingerprint() []string {
	return f.segments
}
