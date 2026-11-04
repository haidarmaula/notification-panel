package installation

import "time"

const (
	// Bound database acquisition and work even when an HTTP caller has no deadline.
	operationTimeout = 5 * time.Second
	// Cleanup must remain possible after the operation context has expired.
	rollbackTimeout = time.Second
)
