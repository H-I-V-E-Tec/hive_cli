package installer

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/H-I-V-E-Tec/hive_cli/internal/registry"
	"github.com/H-I-V-E-Tec/hive_cli/internal/release"
	"github.com/H-I-V-E-Tec/hive_cli/internal/store"
)

var testProduct = registry.Product{
	Name: "mind", Repo: "H-I-V-E-Tec/hive_mind", AssetPrefix: "hive",
	ArchiveBinary: "hive", Command: "hive-mind", SmokeArgs: []string{"version"},
}

type fakeReleases struct {
	latest  string
	assets  map[string][]byte
	fetched []string
}

func (f *fakeReleases) Latest(context.Context, string) (string, error) { return f.latest, nil }

func (f *fakeReleases) Asset(_ context.Context, _, version, name string, maxBytes int64) ([]byte, error) {
	f.fetched = append(f.fetched, version+"/"+name)
	data, ok := f.assets[version+"/"+name]
	if !ok {
		return nil, release.ErrNotFound
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("too large")
	}
	return data, nil
}

type fakeSignatures struct {
	err        error
	identities []string
}

func (f *fakeSignatures) VerifyBundle(_, _ []byte, san string) error {
	f.identities = append(f.identities, san)
	return f.err
}

// fakeSmoke treats the binary content "bin:<version>" as its reported version.
func fakeSmoke(_ context.Context, binary string, _ []string) (string, error) {
	data, err := os.ReadFile(binary)
	if err != nil {
		return "", err
	}
	if !bytes.HasPrefix(data, []byte("bin:")) {
		return "", errors.New("binário não executa")
	}
	return string(bytes.TrimPrefix(data, []byte("bin:"))), nil
}

func tarGz(t *testing.T, entries map[string][]byte, extra func(*tar.Writer)) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, data := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write(data)
	}
	if extra != nil {
		extra(tw)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func publish(t *testing.T, rel *fakeReleases, version string, archive []byte) {
	t.Helper()
	asset := testProduct.AssetName(version, "linux", "amd64")
	sum := sha256.Sum256(archive)
	rel.assets[version+"/"+asset] = archive
	rel.assets[version+"/"+SumsName] = []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), asset))
	rel.assets[version+"/"+BundleName] = []byte(`{"fake":"bundle"}`)
}

func publishVersion(t *testing.T, rel *fakeReleases, version string) {
	publish(t, rel, version, tarGz(t, map[string][]byte{"hive": []byte("bin:" + version)}, nil))
}

func newTestInstaller(t *testing.T) (*Installer, *fakeReleases, *fakeSignatures) {
	t.Helper()
	rel := &fakeReleases{assets: map[string][]byte{}}
	sig := &fakeSignatures{}
	st := &store.Store{Root: filepath.Join(t.TempDir(), "hive")}
	return &Installer{Store: st, Releases: rel, Signatures: sig, Smoke: fakeSmoke, GOOS: "linux", GOARCH: "amd64"}, rel, sig
}

func activeVersion(t *testing.T, in *Installer) string {
	t.Helper()
	st, err := in.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	ps, ok := st.Installed("mind")
	if !ok {
		return ""
	}
	return ps.Active
}

func TestInstallVerifiesAndActivates(t *testing.T) {
	in, rel, sig := newTestInstaller(t)
	publishVersion(t, rel, "v1.0.0")
	rel.latest = "v1.0.0"

	res, err := in.Install(context.Background(), testProduct, "", false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !res.Changed || res.Version != "v1.0.0" || activeVersion(t, in) != "v1.0.0" {
		t.Fatalf("unexpected result %+v / active %q", res, activeVersion(t, in))
	}
	want := "https://github.com/H-I-V-E-Tec/hive_mind/.github/workflows/release.yml@refs/tags/v1.0.0"
	if len(sig.identities) != 1 || sig.identities[0] != want {
		t.Fatalf("signature identity = %v, want %s", sig.identities, want)
	}
	info, err := os.Stat(in.BinaryPath(testProduct, "v1.0.0"))
	if err != nil {
		t.Fatalf("binary missing: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("binary not executable: %v", info.Mode())
	}
}

func TestSignatureFailureStopsBeforeDownloadingArchive(t *testing.T) {
	in, rel, sig := newTestInstaller(t)
	publishVersion(t, rel, "v1.0.0")
	sig.err = errors.New("identity mismatch")

	if _, err := in.Install(context.Background(), testProduct, "v1.0.0", false); err == nil {
		t.Fatal("install accepted a bad signature")
	}
	for _, f := range rel.fetched {
		if strings.HasSuffix(f, ".tar.gz") {
			t.Fatalf("archive downloaded before signature check: %v", rel.fetched)
		}
	}
	if activeVersion(t, in) != "" {
		t.Fatal("product activated despite bad signature")
	}
}

func TestTamperedArchiveRejected(t *testing.T) {
	in, rel, _ := newTestInstaller(t)
	publishVersion(t, rel, "v1.0.0")
	asset := "v1.0.0/" + testProduct.AssetName("v1.0.0", "linux", "amd64")
	rel.assets[asset] = append(rel.assets[asset], 'x')

	_, err := in.Install(context.Background(), testProduct, "v1.0.0", false)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum error, got %v", err)
	}
	if activeVersion(t, in) != "" {
		t.Fatal("tampered archive activated")
	}
}

func TestMissingStandardBundleRejected(t *testing.T) {
	in, rel, _ := newTestInstaller(t)
	publishVersion(t, rel, "v1.0.0")
	delete(rel.assets, "v1.0.0/"+BundleName)

	_, err := in.Install(context.Background(), testProduct, "v1.0.0", false)
	if err == nil || !strings.Contains(err.Error(), BundleName) {
		t.Fatalf("expected missing bundle error, got %v", err)
	}
}

func TestMissingPlatformIsExplained(t *testing.T) {
	in, rel, _ := newTestInstaller(t)
	publishVersion(t, rel, "v1.0.0")
	in.GOARCH = "riscv64"

	_, err := in.Install(context.Background(), testProduct, "v1.0.0", false)
	if err == nil || !strings.Contains(err.Error(), "linux/riscv64") {
		t.Fatalf("expected platform error, got %v", err)
	}
}

func TestDowngradeRequiresFlag(t *testing.T) {
	in, rel, _ := newTestInstaller(t)
	publishVersion(t, rel, "v1.0.0")
	publishVersion(t, rel, "v2.0.0")
	ctx := context.Background()
	if _, err := in.Install(ctx, testProduct, "v2.0.0", false); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Install(ctx, testProduct, "v1.0.0", false); err == nil {
		t.Fatal("downgrade accepted without --allow-downgrade")
	}
	if _, err := in.Install(ctx, testProduct, "v1.0.0", true); err != nil {
		t.Fatalf("explicit downgrade failed: %v", err)
	}
	if got := activeVersion(t, in); got != "v1.0.0" {
		t.Fatalf("active = %s", got)
	}
}

func TestFailedSmokeTestKeepsPreviousVersion(t *testing.T) {
	in, rel, _ := newTestInstaller(t)
	ctx := context.Background()
	publishVersion(t, rel, "v1.0.0")
	if _, err := in.Install(ctx, testProduct, "v1.0.0", false); err != nil {
		t.Fatal(err)
	}
	publish(t, rel, "v1.1.0", tarGz(t, map[string][]byte{"hive": []byte("garbage")}, nil))

	if _, err := in.Install(ctx, testProduct, "v1.1.0", false); err == nil {
		t.Fatal("broken binary activated")
	}
	if got := activeVersion(t, in); got != "v1.0.0" {
		t.Fatalf("active = %s, want v1.0.0", got)
	}
	if _, err := os.Stat(in.Store.VersionDir("mind", "v1.1.0")); !os.IsNotExist(err) {
		t.Fatal("broken version left on disk")
	}
	entries, _ := os.ReadDir(in.Store.ProductsDir("mind"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".staging-") {
			t.Fatalf("staging directory left behind: %s", e.Name())
		}
	}
}

func TestSmokeVersionMismatchRejected(t *testing.T) {
	in, rel, _ := newTestInstaller(t)
	publish(t, rel, "v1.0.0", tarGz(t, map[string][]byte{"hive": []byte("bin:v0.9.0")}, nil))
	if _, err := in.Install(context.Background(), testProduct, "v1.0.0", false); err == nil {
		t.Fatal("binary reporting another version was activated")
	}
}

func TestUpgradeKeepsOnePreviousAndRollback(t *testing.T) {
	in, rel, _ := newTestInstaller(t)
	ctx := context.Background()
	for _, v := range []string{"v1.0.0", "v1.1.0", "v1.2.0"} {
		publishVersion(t, rel, v)
		if _, err := in.Install(ctx, testProduct, v, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(in.Store.VersionDir("mind", "v1.0.0")); !os.IsNotExist(err) {
		t.Fatal("old version not pruned")
	}
	res, err := in.Rollback(ctx, testProduct)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if res.Version != "v1.1.0" || activeVersion(t, in) != "v1.1.0" {
		t.Fatalf("rollback result %+v, active %s", res, activeVersion(t, in))
	}
	if res, err = in.Rollback(ctx, testProduct); err != nil || res.Version != "v1.2.0" {
		t.Fatalf("rollback back to v1.2.0: %+v %v", res, err)
	}
}

func TestReinstallSameVersionIsNoop(t *testing.T) {
	in, rel, _ := newTestInstaller(t)
	publishVersion(t, rel, "v1.0.0")
	ctx := context.Background()
	in.Install(ctx, testProduct, "v1.0.0", false)
	rel.fetched = nil
	res, err := in.Install(ctx, testProduct, "v1.0.0", false)
	if err != nil || res.Changed || len(rel.fetched) != 0 {
		t.Fatalf("reinstall should be a no-op: %+v %v %v", res, err, rel.fetched)
	}
}

func TestUninstallRemovesProduct(t *testing.T) {
	in, rel, _ := newTestInstaller(t)
	publishVersion(t, rel, "v1.0.0")
	in.Install(context.Background(), testProduct, "v1.0.0", false)
	if err := in.Uninstall(testProduct); err != nil {
		t.Fatal(err)
	}
	if activeVersion(t, in) != "" {
		t.Fatal("still active after uninstall")
	}
	if _, err := os.Stat(in.Store.ProductsDir("mind")); !os.IsNotExist(err) {
		t.Fatal("product directory kept")
	}
}

func TestConcurrentOperationIsRejected(t *testing.T) {
	in, rel, _ := newTestInstaller(t)
	publishVersion(t, rel, "v1.0.0")
	if err := in.Store.Ensure(); err != nil {
		t.Fatal(err)
	}
	unlock, err := in.Store.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := in.Install(context.Background(), testProduct, "v1.0.0", false); err == nil || !strings.Contains(err.Error(), "andamento") {
		t.Fatalf("expected lock error, got %v", err)
	}
}

func TestMaliciousArchivesRejected(t *testing.T) {
	cases := map[string][]byte{
		// The valid binary is present, so only the path rule can reject these.
		"path traversal": tarGz(t, map[string][]byte{"hive": []byte("bin:v1.0.0"), "../evil": []byte("x")}, nil),
		"absolute path":  tarGz(t, map[string][]byte{"hive": []byte("bin:v1.0.0"), "/etc/evil": []byte("x")}, nil),
		"backslash":      tarGz(t, map[string][]byte{"hive": []byte("bin:v1.0.0"), `..\evil`: []byte("x")}, nil),
		"symlink": tarGz(t, map[string][]byte{"hive": []byte("bin:v1.0.0")}, func(tw *tar.Writer) {
			tw.WriteHeader(&tar.Header{Name: "link", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink})
		}),
		"duplicate binary": func() []byte {
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			tw := tar.NewWriter(gz)
			for i := 0; i < 2; i++ {
				tw.WriteHeader(&tar.Header{Name: "hive", Mode: 0o755, Size: 10, Typeflag: tar.TypeReg})
				tw.Write([]byte("bin:v1.0.0"))
			}
			tw.Close()
			gz.Close()
			return buf.Bytes()
		}(),
		"missing binary": tarGz(t, map[string][]byte{"other": []byte("x")}, nil),
	}
	for name, archive := range cases {
		t.Run(name, func(t *testing.T) {
			in, rel, _ := newTestInstaller(t)
			publish(t, rel, "v1.0.0", archive)
			if _, err := in.Install(context.Background(), testProduct, "v1.0.0", false); err == nil {
				t.Fatal("malicious archive accepted")
			}
			if activeVersion(t, in) != "" {
				t.Fatal("malicious archive activated")
			}
		})
	}
}

func TestZipSymlinkRejected(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: "hive.exe"}
	hdr.SetMode(os.ModeSymlink | 0o777)
	w, _ := zw.CreateHeader(hdr)
	w.Write([]byte("C:/Windows/System32/cmd.exe"))
	zw.Close()
	if _, err := extractBinary(buf.Bytes(), true, "hive.exe", 1<<20); err == nil {
		t.Fatal("zip symlink accepted")
	}
}

func TestZipExtraction(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("hive.exe")
	w.Write([]byte("bin:v1.0.0"))
	zw.Close()
	got, err := extractBinary(buf.Bytes(), true, "hive.exe", 1<<20)
	if err != nil || string(got) != "bin:v1.0.0" {
		t.Fatalf("zip extraction: %q %v", got, err)
	}
}

func TestOversizedBinaryRejected(t *testing.T) {
	archive := tarGz(t, map[string][]byte{"hive": bytes.Repeat([]byte("a"), 2048)}, nil)
	if _, err := extractBinary(archive, false, "hive", 1024); err == nil {
		t.Fatal("oversized binary accepted")
	}
}
