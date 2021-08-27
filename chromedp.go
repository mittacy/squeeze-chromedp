package squeeze_chromedp

import (
	"context"
)

// Conn represents a connection to a chrome_dp conn.
type Conn interface {
	// Close closes the connection.
	Close() error

	// Err returns a non-nil value when the connection is not usable.
	Err() error

	// Get returns a context when the Err is nil.
	Get() context.Context
}
