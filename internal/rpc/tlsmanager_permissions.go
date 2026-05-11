package rpc

import (
	"fmt"
	"os"
)

// checkKeyFilePermissions verifies the TLS key file has no group or other
// permission bits set (matching the OpenSSH approach: mode & 0077 != 0).
// Returns an error if the file is accessible by group or others, which
// would expose the private key to unauthorized users.
func checkKeyFilePermissions(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot stat key file %s: %w", path, err)
	}
	mode := info.Mode().Perm()
	if mode&0077 != 0 {
		return fmt.Errorf("TLS key file %s has mode %04o, must not be accessible by group or others (maximum 0600)", path, mode)
	}
	return nil
}
