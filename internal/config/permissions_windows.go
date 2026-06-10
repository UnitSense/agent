//go:build windows

package config

import "os"

// Windows does not map Unix permission bits via Mode().Perm(), so the Unix
// group/other check is a no-op here. File security is governed by NTFS ACLs.
// TODO: use golang.org/x/sys/windows/security to verify the DACL grants
// access only to the owning user (equivalent to the Unix 0600 check).
func checkPermissions(_ os.FileInfo, _ string) error {
	return nil
}
