//go:build windows

package schedule

import (
	"errors"
	"time"
)

func Install(binPath string, interval time.Duration) error {
	_ = binPath
	_ = interval
	return errors.New("Windows installer ships in v0.1; run `unitsense-agent.exe run` manually")
}

func Uninstall() error {
	return errors.New("Windows uninstaller ships in v0.1")
}
