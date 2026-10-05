//go:build !windows

package dispatch

import (
	"os"
	"syscall"
)

// Run replaces the launcher process with the product so stdio (the MCP
// channel) and signals go straight to it; it only returns on failure.
func Run(binary string, args []string, env []string) (int, error) {
	err := syscall.Exec(binary, append([]string{binary}, args...), env)
	return 1, &os.PathError{Op: "exec", Path: binary, Err: err}
}
