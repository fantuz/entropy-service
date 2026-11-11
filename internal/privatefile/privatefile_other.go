//go:build !windows

// Package privatefile creates new private files without replacing existing paths.
package privatefile

import (
	"errors"
	"os"
)

func Create(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

// Check verifies the platform's private regular-file policy without changing it.
func Check(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}

	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("private file must be regular, without symlinks or group/other permissions")
	}

	return nil
}
