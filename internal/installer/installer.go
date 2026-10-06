// Package installer downloads, verifies, smoke-tests and activates product
// versions. A version only becomes active after every check has passed.
package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/mod/semver"

	"github.com/H-I-V-E-Tec/hive_cli/internal/dispatch"
	"github.com/H-I-V-E-Tec/hive_cli/internal/registry"
	"github.com/H-I-V-E-Tec/hive_cli/internal/release"
	"github.com/H-I-V-E-Tec/hive_cli/internal/store"
	"github.com/H-I-V-E-Tec/hive_cli/internal/verify"
)

const (
	SumsName   = "SHA256SUMS"
	BundleName = "SHA256SUMS.sigstore-bundle.json"

	maxSmallFile = 1 << 20
	maxArchive   = 512 << 20
	maxBinary    = 512 << 20
	keepPrevious = 1
	smokeTimeout = 30 * time.Second
)

// Releases is the subset of the release client the installer needs.
type Releases interface {
	Latest(ctx context.Context, repo string) (string, error)
	Asset(ctx context.Context, repo, version, name string, maxBytes int64) ([]byte, error)
}

// SmokeFunc runs an installed binary and returns the version it reports.
type SmokeFunc func(ctx context.Context, binary string, args []string) (string, error)

type Installer struct {
	Store      *store.Store
	Releases   Releases
	Signatures verify.Signatures
	Smoke      SmokeFunc
	GOOS       string
	GOARCH     string
	Log        io.Writer
}

type Result struct {
	Version  string
	Previous string
	Changed  bool
}

func (in *Installer) logf(format string, args ...any) {
	if in.Log != nil {
		fmt.Fprintf(in.Log, format+"\n", args...)
	}
}

// Latest asks GitHub for the newest release and caches the answer in state.
func (in *Installer) Latest(ctx context.Context, p registry.Product) (string, error) {
	latest, err := in.Releases.Latest(ctx, p.Repo)
	if err != nil {
		return "", err
	}
	if p.Name != registry.Launcher.Name {
		if unlock, lockErr := in.Store.Lock(); lockErr == nil {
			if st, loadErr := in.Store.Load(); loadErr == nil {
				ps := st.Product(p.Name)
				ps.Latest, ps.CheckedAt = latest, time.Now().UTC()
				_ = in.Store.Save(st)
			}
			unlock()
		}
	}
	return latest, nil
}

// FetchVerified returns the product executable for version after checking the
// Sigstore signature of SHA256SUMS and the archive digest it lists.
func (in *Installer) FetchVerified(ctx context.Context, p registry.Product, version string) ([]byte, error) {
	if !semver.IsValid(version) {
		return nil, fmt.Errorf("versão inválida: %q", version)
	}
	sums, err := in.Releases.Asset(ctx, p.Repo, version, SumsName, maxSmallFile)
	if err != nil {
		return nil, err
	}
	bundleJSON, err := in.Releases.Asset(ctx, p.Repo, version, BundleName, maxSmallFile)
	if errors.Is(err, release.ErrNotFound) {
		return nil, fmt.Errorf("%s %s não publica %s; esta versão não pode ser verificada pelo launcher", p.Name, version, BundleName)
	}
	if err != nil {
		return nil, err
	}
	if err := in.Signatures.VerifyBundle(sums, bundleJSON, p.SignerIdentity(version)); err != nil {
		return nil, err
	}
	in.logf("✓ assinatura de %s verificada (%s)", SumsName, p.SignerIdentity(version))

	asset := p.AssetName(version, in.GOOS, in.GOARCH)
	expected, err := verify.ExpectedSHA256(sums, asset)
	if err != nil {
		return nil, fmt.Errorf("%w (plataforma %s/%s sem pacote nesta versão?)", err, in.GOOS, in.GOARCH)
	}
	archive, err := in.Releases.Asset(ctx, p.Repo, version, asset, maxArchive)
	if err != nil {
		return nil, err
	}
	if err := verify.CheckSHA256(archive, expected); err != nil {
		return nil, err
	}
	in.logf("✓ checksum de %s verificado", asset)
	if p.Runtime == registry.PythonZipapp {
		// The complete zipapp is installed as a single file; nothing is extracted.
		return archive, nil
	}
	return extractBinary(archive, in.GOOS == "windows", registry.ExecutableName(p.ArchiveBinary, in.GOOS), maxBinary)
}

// Install activates version (or the latest release when version is empty).
func (in *Installer) Install(ctx context.Context, p registry.Product, version string, allowDowngrade bool) (Result, error) {
	if err := in.Store.Ensure(); err != nil {
		return Result{}, err
	}
	if version == "" {
		latest, err := in.Latest(ctx, p)
		if err != nil {
			return Result{}, err
		}
		version = latest
	}
	if !semver.IsValid(version) {
		return Result{}, fmt.Errorf("versão inválida: %q (use o formato vX.Y.Z)", version)
	}

	unlock, err := in.Store.Lock()
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	st, err := in.Store.Load()
	if err != nil {
		return Result{}, err
	}
	ps := st.Product(p.Name)
	if ps.Active == version {
		return Result{Version: version}, nil
	}
	if ps.Active != "" && semver.Compare(version, ps.Active) < 0 && !allowDowngrade {
		return Result{}, fmt.Errorf("%s %s é mais antiga que a instalada (%s); use --allow-downgrade se for intencional, ou `hive rollback %s`", p.Name, version, ps.Active, p.Name)
	}

	binary, err := in.FetchVerified(ctx, p, version)
	if err != nil {
		return Result{}, err
	}

	productsDir := in.Store.ProductsDir(p.Name)
	if err := os.MkdirAll(productsDir, 0o700); err != nil {
		return Result{}, err
	}
	staging, err := os.MkdirTemp(productsDir, ".staging-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(staging)
	exeName := p.InstalledName(in.GOOS)
	mode := os.FileMode(0o755)
	if p.Runtime == registry.PythonZipapp {
		mode = 0o644
	}
	if err := os.WriteFile(filepath.Join(staging, exeName), binary, mode); err != nil {
		return Result{}, err
	}
	if err := in.smoke(ctx, p, filepath.Join(staging, exeName), version); err != nil {
		return Result{}, err
	}

	final := in.Store.VersionDir(p.Name, version)
	if err := os.RemoveAll(final); err != nil {
		return Result{}, err
	}
	if err := os.Rename(staging, final); err != nil {
		return Result{}, err
	}

	previous := ps.Active
	if previous != "" {
		ps.Previous = append([]string{previous}, ps.Previous...)
		if len(ps.Previous) > keepPrevious {
			ps.Previous = ps.Previous[:keepPrevious]
		}
	}
	ps.Active = version
	if err := in.Store.Save(st); err != nil {
		return Result{}, err
	}
	in.prune(p.Name, ps)
	return Result{Version: version, Previous: previous, Changed: true}, nil
}

func (in *Installer) smoke(ctx context.Context, p registry.Product, binary, version string) error {
	ctx, cancel := context.WithTimeout(ctx, smokeTimeout)
	defer cancel()
	command, args, err := dispatch.ProductCommand(p, binary, p.SmokeArgs, in.GOOS)
	if err != nil {
		return err
	}
	reported, err := in.Smoke(ctx, command, args)
	if err != nil {
		return fmt.Errorf("teste do binário falhou (%s): %w", binary, err)
	}
	if reported != version {
		return fmt.Errorf("binário informa versão %q, esperada %q", reported, version)
	}
	return nil
}

// Rollback re-activates the most recent previous version still on disk.
func (in *Installer) Rollback(ctx context.Context, p registry.Product) (Result, error) {
	unlock, err := in.Store.Lock()
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	st, err := in.Store.Load()
	if err != nil {
		return Result{}, err
	}
	ps, ok := st.Installed(p.Name)
	if !ok {
		return Result{}, fmt.Errorf("%s não está instalado", p.Name)
	}
	if len(ps.Previous) == 0 {
		return Result{}, fmt.Errorf("não há versão anterior de %s para restaurar", p.Name)
	}
	target := ps.Previous[0]
	binary := in.BinaryPath(p, target)
	if err := in.smoke(ctx, p, binary, target); err != nil {
		return Result{}, err
	}
	current := ps.Active
	ps.Active = target
	ps.Previous = []string{current}
	if err := in.Store.Save(st); err != nil {
		return Result{}, err
	}
	return Result{Version: target, Previous: current, Changed: true}, nil
}

func (in *Installer) Uninstall(p registry.Product) error {
	unlock, err := in.Store.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	st, err := in.Store.Load()
	if err != nil {
		return err
	}
	if _, ok := st.Installed(p.Name); !ok {
		return fmt.Errorf("%s não está instalado", p.Name)
	}
	if err := os.RemoveAll(in.Store.ProductsDir(p.Name)); err != nil {
		return err
	}
	delete(st.Products, p.Name)
	return in.Store.Save(st)
}

func (in *Installer) BinaryPath(p registry.Product, version string) string {
	return filepath.Join(in.Store.VersionDir(p.Name, version), p.InstalledName(in.GOOS))
}

// prune keeps only the active and retained previous versions on disk.
func (in *Installer) prune(name string, ps *store.ProductState) {
	keep := map[string]bool{ps.Active: true}
	for _, v := range ps.Previous {
		keep[v] = true
	}
	entries, err := os.ReadDir(in.Store.ProductsDir(name))
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && semver.IsValid(e.Name()) && !keep[e.Name()] {
			_ = os.RemoveAll(filepath.Join(in.Store.ProductsDir(name), e.Name()))
		}
	}
}

// RunVersion executes `binary args...` and reads {"version": ...} from stdout.
func RunVersion(ctx context.Context, binary string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	var payload struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		return "", fmt.Errorf("saída de versão não é JSON: %w", err)
	}
	return payload.Version, nil
}
