//go:build !unix

package deployment

import "os"

// Deployment-file ownership is deliberately unsupported outside Unix.
func rootOwned(os.FileInfo) bool { return false }
