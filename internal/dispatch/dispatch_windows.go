//go:build windows

package dispatch

import (
	"errors"
	"os"
	"os/exec"
)

// Run starts the product with the launcher's stdio and returns its exit code;
// Windows has no exec(2), so the launcher stays as a thin parent process.
func Run(binary string, args []string, env []string) (int, error) {
	cmd := exec.Command(binary, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = env
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}
