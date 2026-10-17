package ports

import "time"

// Clock is the service's notion of "now". Use cases stamp requests, events and
// policy declarations with it.
type Clock interface {
	Now() time.Time
}

// IDGenerator mints request ids.
type IDGenerator interface {
	// NewRequestID returns a fresh "repl-" id (at most 64 characters, no
	// white space, no '/').
	NewRequestID() string
}
