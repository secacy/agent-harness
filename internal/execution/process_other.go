//go:build !darwin && !linux

package execution

import (
	"errors"
	"os/exec"
)

func configureProcess(*exec.Cmd) error {
	return errors.New("local Go checks require macOS or Linux process groups")
}

func terminateProcessGroup(*exec.Cmd) error { return nil }
