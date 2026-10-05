// Package store owns the launcher's private directory (~/.hive): installed
// product versions, the state file and the operation lock.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

const staleLockAfter = 15 * time.Minute

type ProductState struct {
	Active    string    `json:"active"`
	Previous  []string  `json:"previous,omitempty"`
	Latest    string    `json:"latest,omitempty"`
	CheckedAt time.Time `json:"checked_at,omitempty"`
}

type State struct {
	Products map[string]*ProductState `json:"products"`
}

func (s *State) Product(name string) *ProductState {
	if s.Products == nil {
		s.Products = map[string]*ProductState{}
	}
	ps, ok := s.Products[name]
	if !ok {
		ps = &ProductState{}
		s.Products[name] = ps
	}
	return ps
}

// Installed reports the active version without creating an entry.
func (s *State) Installed(name string) (*ProductState, bool) {
	ps, ok := s.Products[name]
	return ps, ok && ps.Active != ""
}

type Store struct {
	Root string
}

// Default is $HIVE_HOME, or ~/.hive (shared with `hive login`'s token).
func Default() (*Store, error) {
	if dir := os.Getenv("HIVE_HOME"); dir != "" {
		if !filepath.IsAbs(dir) {
			return nil, errors.New("HIVE_HOME deve ser um caminho absoluto")
		}
		return &Store{Root: dir}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &Store{Root: filepath.Join(home, ".hive")}, nil
}

func (s *Store) BinDir() string                     { return filepath.Join(s.Root, "bin") }
func (s *Store) ProductsDir(name string) string     { return filepath.Join(s.Root, "products", name) }
func (s *Store) VersionDir(name, ver string) string { return filepath.Join(s.ProductsDir(name), ver) }
func (s *Store) SigstoreCache() string              { return filepath.Join(s.Root, "sigstore") }
func (s *Store) statePath() string                  { return filepath.Join(s.Root, "state.json") }

// Ensure creates the private tree and refuses one other users could write to,
// since anything placed there would be executed as this user.
func (s *Store) Ensure() error {
	for _, dir := range []string{s.Root, s.BinDir(), filepath.Join(s.Root, "products")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	info, err := os.Stat(s.Root)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s pode ser alterado por outros usuários (modo %o); corrija com: chmod 700 %s", s.Root, info.Mode().Perm(), s.Root)
	}
	return nil
}

func (s *Store) Load() (*State, error) {
	data, err := os.ReadFile(s.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return &State{Products: map[string]*ProductState{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("state.json corrompido: %w", err)
	}
	if st.Products == nil {
		st.Products = map[string]*ProductState{}
	}
	return &st, nil
}

func (s *Store) Save(st *State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Root, ".state-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.statePath())
}

// Lock serializes install/update/rollback across processes. A lock left by a
// crashed process is reclaimed after staleLockAfter.
func (s *Store) Lock() (func(), error) {
	path := filepath.Join(s.Root, "lock")
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = f.WriteString(strconv.Itoa(os.Getpid()))
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		info, statErr := os.Stat(path)
		if statErr == nil && time.Since(info.ModTime()) > staleLockAfter {
			_ = os.Remove(path)
			continue
		}
		break
	}
	return nil, fmt.Errorf("outra operação do hive está em andamento (lock em %s)", path)
}
