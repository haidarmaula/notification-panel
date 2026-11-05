//go:build !unix

package requeue

import "os"

// Deployment-file ownership is deliberately unsupported outside Unix.
func rootOwned(os.FileInfo) bool { return false }
