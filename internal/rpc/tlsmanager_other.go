//go:build !unix

package rpc

// checkKeyFileOwnership is a no-op on non-Unix platforms.
// UID-based ownership checks are not available on Windows.
func checkKeyFileOwnership(_ string) error { return nil }
