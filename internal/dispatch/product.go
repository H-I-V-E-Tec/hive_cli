package dispatch

import (
	"fmt"
	"os/exec"

	"github.com/H-I-V-E-Tec/hive_cli/internal/registry"
)

// ProductCommand resolves a product to its native command or Python runtime.
// The same invocation is used for installation smoke tests and MCP dispatch.
func ProductCommand(p registry.Product, binary string, args []string, goos string) (string, []string, error) {
	return productCommand(p, binary, args, goos, exec.LookPath)
}

func productCommand(p registry.Product, binary string, args []string, goos string, lookup func(string) (string, error)) (string, []string, error) {
	if p.Runtime == "" {
		return binary, args, nil
	}
	if p.Runtime != registry.PythonZipapp {
		return "", nil, fmt.Errorf("runtime desconhecido para %s: %s", p.Name, p.Runtime)
	}
	candidates := []string{"python3", "python"}
	if goos == "windows" {
		candidates = []string{"py", "python3", "python"}
	}
	for _, name := range candidates {
		interpreter, err := lookup(name)
		if err != nil {
			continue
		}
		prefix := []string{"-I", binary}
		if name == "py" {
			prefix = []string{"-3", "-I", binary}
		}
		return interpreter, append(prefix, args...), nil
	}
	return "", nil, fmt.Errorf("%s exige Python 3.10 ou superior no PATH (python3 ou python; no Windows, py -3)", p.Name)
}
