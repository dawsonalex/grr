package errs

// Contexter is implemented by errors that contribute structured context to an
// error report. Map keys are context block names; values are flat key→value
// maps that a reporting layer can attach to an event as named sections.
type Contexter interface {
	ErrorContext() map[string]map[string]any
}

// ErrContext is an embeddable type for attaching structured context to an error.
// The zero value is safe — the internal map is initialised lazily on first write.
// Prefer domain-specific helper methods (e.g. ForUser) over calling AddContext
// directly from outside the error type; this keeps context keys consistent and
// their presence part of the type's API.
type ErrContext struct {
	data map[string]map[string]any
}

func (e ErrContext) ErrorContext() map[string]map[string]any {
	return e.data
}

// AddContext sets a named context block on the error. val should be a flat
// map of string keys to values.
func (e *ErrContext) AddContext(key string, val map[string]any) {
	if e.data == nil {
		e.data = make(map[string]map[string]any)
	}
	e.data[key] = val
}
