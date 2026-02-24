package neon

// SafeError wraps a cause with an error string safe for default production
// logging. The wrapped cause may still contain sensitive detail.
type SafeError struct {
	msg   string
	cause error
}

func (e *SafeError) Error() string { return e.msg }
func (e *SafeError) Unwrap() error { return e.cause }
