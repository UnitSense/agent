//go:build !windows

package config

import (
	"fmt"
	"os"
)

func checkPermissions(info os.FileInfo, path string) error {
	if info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("config file %s has loose permissions %o; expected 0600", path, info.Mode().Perm())
	}
	return nil
}
