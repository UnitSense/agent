//go:build windows

package config

import "os"

// Windows does not map Unix permission bits via Mode().Perm().
// File security is governed by Windows ACLs, not checked here.
func checkPermissions(_ os.FileInfo, _ string) error {
	return nil
}
