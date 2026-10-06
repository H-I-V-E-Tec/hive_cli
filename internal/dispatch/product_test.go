package dispatch

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/H-I-V-E-Tec/hive_cli/internal/registry"
)

func TestPythonProductCommand(t *testing.T) {
	p, _ := registry.Lookup("atlas")
	for _, tc := range []struct {
		name, goos, available string
		prefix                []string
	}{
		{"Linux", "linux", "python3", []string{"-I", "/private/hive-atlas.pyz"}},
		{"macOS fallback", "darwin", "python", []string{"-I", "/private/hive-atlas.pyz"}},
		{"Windows py", "windows", "py", []string{"-3", "-I", "/private/hive-atlas.pyz"}},
		{"Windows python", "windows", "python", []string{"-I", "/private/hive-atlas.pyz"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lookup := func(name string) (string, error) {
				if name == tc.available {
					return "/runtime/" + name, nil
				}
				return "", errors.New("not found")
			}
			command, args, err := productCommand(p, "/private/hive-atlas.pyz", []string{"version", "--json"}, tc.goos, lookup)
			wantArgs := append(tc.prefix, "version", "--json")
			if err != nil || command != "/runtime/"+tc.available || !reflect.DeepEqual(args, wantArgs) {
				t.Fatalf("command=%s args=%v err=%v", command, args, err)
			}
		})
	}
}

func TestMissingPythonExplained(t *testing.T) {
	p, _ := registry.Lookup("atlas")
	_, _, err := productCommand(p, "/atlas.pyz", nil, "linux", func(string) (string, error) {
		return "", errors.New("not found")
	})
	if err == nil || !strings.Contains(err.Error(), "Python 3.10") {
		t.Fatalf("missing Python error: %v", err)
	}
}

func TestNativeProductDoesNotResolvePython(t *testing.T) {
	p, _ := registry.Lookup("mind")
	command, args, err := productCommand(p, "/mind", []string{"search"}, "linux", func(string) (string, error) {
		t.Fatal("native product attempted runtime lookup")
		return "", nil
	})
	if err != nil || command != "/mind" || !reflect.DeepEqual(args, []string{"search"}) {
		t.Fatalf("command=%s args=%v err=%v", command, args, err)
	}
}
