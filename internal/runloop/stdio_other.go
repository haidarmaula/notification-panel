//go:build !linux && !darwin

package runloop

// PrepareCommandOutputs leaves native standard stream ownership unchanged.
func PrepareCommandOutputs() (func(), error) { return func() {}, nil }

// PrepareCommandInput leaves native standard input ownership unchanged.
func PrepareCommandInput() (func(), error) { return func() {}, nil }
