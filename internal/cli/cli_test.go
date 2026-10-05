package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/H-I-V-E-Tec/hive_cli/internal/store"
)

type dispatched struct {
	binary string
	args   []string
	env    []string
}

func newTestApp(t *testing.T) (*App, *dispatched, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	st := &store.Store{Root: filepath.Join(t.TempDir(), "hive")}
	if err := st.Ensure(); err != nil {
		t.Fatal(err)
	}
	got := &dispatched{}
	var stdout, stderr bytes.Buffer
	app := &App{
		Version:  "v0.1.0",
		Store:    st,
		Launcher: "/home/u/.hive/bin/hive",
		Environ:  []string{"PATH=/bin", "HIVE_LAUNCHER=/old/path"},
		GOOS:     "linux",
		Stdout:   &stdout,
		Stderr:   &stderr,
		Dispatch: func(binary string, args, env []string) (int, error) {
			got.binary, got.args, got.env = binary, args, env
			return 7, nil
		},
	}
	return app, got, &stdout, &stderr
}

func installState(t *testing.T, app *App, active string, previous ...string) {
	t.Helper()
	st, _ := app.Store.Load()
	ps := st.Product("mind")
	ps.Active, ps.Previous = active, previous
	if err := app.Store.Save(st); err != nil {
		t.Fatal(err)
	}
}

func TestProductDispatchUsesActiveVersionAndLauncherEnv(t *testing.T) {
	app, got, _, _ := newTestApp(t)
	installState(t, app, "v1.3.10")

	code := app.Run([]string{"mind", "search", "acme", "login"})
	if code != 7 {
		t.Fatalf("exit code not propagated: %d", code)
	}
	want := filepath.Join(app.Store.Root, "products", "mind", "v1.3.10", "hive-mind")
	if got.binary != want {
		t.Fatalf("binary = %s, want %s", got.binary, want)
	}
	if strings.Join(got.args, " ") != "search acme login" {
		t.Fatalf("args = %v", got.args)
	}
	launcherVars := 0
	for _, kv := range got.env {
		if strings.HasPrefix(kv, "HIVE_LAUNCHER=") {
			launcherVars++
			if kv != "HIVE_LAUNCHER=/home/u/.hive/bin/hive" {
				t.Fatalf("stale HIVE_LAUNCHER passed: %s", kv)
			}
		}
	}
	if launcherVars != 1 {
		t.Fatalf("HIVE_LAUNCHER set %d times", launcherVars)
	}
}

func TestBareProductStartsMCP(t *testing.T) {
	app, got, _, _ := newTestApp(t)
	installState(t, app, "v1.3.10")
	app.Run([]string{"mind"})
	if len(got.args) != 0 {
		t.Fatalf("`hive mind` must run the product with no args (MCP), got %v", got.args)
	}
}

func TestUnknownCommandsAreForwardedToMind(t *testing.T) {
	app, got, _, _ := newTestApp(t)
	installState(t, app, "v1.3.10")
	for _, args := range [][]string{{"login", "--center-url", "https://c"}, {"setup", "codex"}, {"doctor"}} {
		app.Run(args)
		if strings.Join(got.args, " ") != strings.Join(args, " ") || !strings.HasSuffix(got.binary, "hive-mind") {
			t.Fatalf("%v not forwarded to mind: %+v", args, got)
		}
	}
}

func TestNotInstalledProductIsExplained(t *testing.T) {
	app, got, _, stderr := newTestApp(t)
	if code := app.Run([]string{"doctor"}); code != exitUsage {
		t.Fatalf("exit = %d", code)
	}
	if got.binary != "" || !strings.Contains(stderr.String(), "hive install mind") {
		t.Fatalf("missing guidance: %q", stderr.String())
	}
}

func TestVersionTable(t *testing.T) {
	app, _, stdout, _ := newTestApp(t)
	installState(t, app, "v1.3.10", "v1.3.9")
	st, _ := app.Store.Load()
	st.Product("mind").Latest, st.Product("mind").CheckedAt = "v1.4.0", time.Now()
	app.Store.Save(st)

	if code := app.Run([]string{"version"}); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	out := stdout.String()
	for _, want := range []string{"hive", "v0.1.0", "mind", "v1.3.10", "anterior: v1.3.9", "disponível: v1.4.0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("version output missing %q:\n%s", want, out)
		}
	}
}

func TestVersionHidesStaleLatest(t *testing.T) {
	app, _, stdout, _ := newTestApp(t)
	installState(t, app, "v1.3.10")
	st, _ := app.Store.Load()
	st.Product("mind").Latest, st.Product("mind").CheckedAt = "v1.4.0", time.Now().Add(-48*time.Hour)
	app.Store.Save(st)
	app.Run([]string{"version"})
	if strings.Contains(stdout.String(), "v1.4.0") {
		t.Fatalf("stale latest shown:\n%s", stdout.String())
	}
}

func TestVersionJSON(t *testing.T) {
	app, _, stdout, _ := newTestApp(t)
	installState(t, app, "v1.3.10")
	if code := app.Run([]string{"version", "--json"}); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	var report versionReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout.String())
	}
	if report.Version != "v0.1.0" || report.Products["mind"].Active != "v1.3.10" {
		t.Fatalf("unexpected report %+v", report)
	}
}

func TestVersionNotInstalled(t *testing.T) {
	app, _, stdout, _ := newTestApp(t)
	app.Run([]string{"version"})
	if !strings.Contains(stdout.String(), "não instalado") {
		t.Fatalf("missing not-installed status:\n%s", stdout.String())
	}
}

func TestUsageErrors(t *testing.T) {
	app, _, _, _ := newTestApp(t)
	for _, args := range [][]string{
		{"install"},
		{"install", "atlas-que-nao-existe"},
		{"install", "mind", "--bogus"},
		{"rollback"},
		{"uninstall"},
		{"version", "--yaml"},
		{"update", "--bogus"},
	} {
		if code := app.Run(args); code != exitUsage {
			t.Errorf("%v: exit %d, want %d", args, code, exitUsage)
		}
	}
}

func TestHelpWithoutArgs(t *testing.T) {
	app, got, stdout, _ := newTestApp(t)
	if code := app.Run(nil); code != exitOK || got.binary != "" || !strings.Contains(stdout.String(), "hive install") {
		t.Fatalf("bare hive should print help: code=%d out=%q", code, stdout.String())
	}
}
