package pipeline

// UserError is a failure caused by the request itself (a missing question, an
// unknown mode) whose message is safe to show to the person who made it. Any
// other error is an internal one: callers that face a user (the HTTP
// daemon) show a generic text and keep the full error in the log.
type UserError struct {
	Msg string
	// Cause is the sentinel behind the message (e.g. ErrLLMStop), so callers
	// that act on it (the eval loop stops on a usage limit) can match with
	// errors.Is; it is never shown.
	Cause error
}

func (e *UserError) Error() string { return e.Msg }

func (e *UserError) Unwrap() error { return e.Cause }
