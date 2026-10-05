package store

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestEnsureCreatesPrivateTree(t *testing.T) {
	s := &Store{Root: filepath.Join(t.TempDir(), "hive")}
	if err := s.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{s.Root, s.BinDir(), filepath.Join(s.Root, "products")} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("%s not created", dir)
		}
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(s.Root)
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("root mode %o, want 700", info.Mode().Perm())
		}
	}
}

func TestEnsureRefusesWritableByOthers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	s := &Store{Root: filepath.Join(t.TempDir(), "hive")}
	os.MkdirAll(s.Root, 0o700)
	os.Chmod(s.Root, 0o777)
	if err := s.Ensure(); err == nil || !strings.Contains(err.Error(), "outros usuários") {
		t.Fatalf("world-writable root accepted: %v", err)
	}
}

func TestStateRoundTripAndAtomicSave(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	st, err := s.Load()
	if err != nil || len(st.Products) != 0 {
		t.Fatalf("empty state: %+v %v", st, err)
	}
	st.Product("mind").Active = "v1.0.0"
	if err := s.Save(st); err != nil {
		t.Fatal(err)
	}
	again, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if ps, ok := again.Installed("mind"); !ok || ps.Active != "v1.0.0" {
		t.Fatalf("state not persisted: %+v", again)
	}
	entries, _ := os.ReadDir(s.Root)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".state-") {
			t.Fatalf("temporary state file left: %s", e.Name())
		}
	}
}

func TestCorruptStateIsReported(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	os.WriteFile(filepath.Join(s.Root, "state.json"), []byte("{"), 0o600)
	if _, err := s.Load(); err == nil {
		t.Fatal("corrupt state accepted")
	}
}

func TestLockIsExclusiveAndReclaimsStale(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	unlock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lock(); err == nil {
		t.Fatal("second lock acquired")
	}
	unlock()
	if unlock2, err := s.Lock(); err != nil {
		t.Fatalf("lock not released: %v", err)
	} else {
		unlock2()
	}

	stale := filepath.Join(s.Root, "lock")
	os.WriteFile(stale, []byte("99999"), 0o600)
	old := time.Now().Add(-2 * staleLockAfter)
	os.Chtimes(stale, old, old)
	if unlock3, err := s.Lock(); err != nil {
		t.Fatalf("stale lock not reclaimed: %v", err)
	} else {
		unlock3()
	}
}

func TestHiveHomeMustBeAbsolute(t *testing.T) {
	t.Setenv("HIVE_HOME", "relative/dir")
	if _, err := Default(); err == nil {
		t.Fatal("relative HIVE_HOME accepted")
	}
}
