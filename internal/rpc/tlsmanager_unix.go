//go:build unix

package rpc

import (
	"fmt"
	"os"
	"syscall"
)

// checkKeyFileOwnership verifies the TLS key file is owned by the effective UID
// of the current process. Prevents using key files owned by other users.
func checkKeyFileOwnership(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot stat key file %s: %w", path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil // non-Unix stat type, skip check
	}
	euid := uint32(os.Geteuid())
	if stat.Uid != euid {
		return fmt.Errorf("TLS key file %s owned by UID %d, expected %d (effective UID)", path, stat.Uid, euid)
	}
	return nil
}
