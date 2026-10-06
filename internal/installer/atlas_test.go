package installer

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/H-I-V-E-Tec/hive_cli/internal/registry"
)

// A real executable zipapp tests Python invocation and staging, without a server.
func publishAtlas(t *testing.T, rel *fakeReleases, tag, reported string) registry.Product {
	t.Helper()
	p, _ := registry.Lookup("atlas")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create("__main__.py")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(f, "import json, sys\nif sys.version_info < (3, 10): sys.exit(2)\nprint(json.dumps({'version': %q}))\n", reported); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	asset := p.AssetName(tag, runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(buf.Bytes())
	rel.assets[tag+"/"+asset] = buf.Bytes()
	rel.assets[tag+"/"+SumsName] = []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), asset))
	rel.assets[tag+"/"+BundleName] = []byte(`{"fake":"bundle"}`)
	return p
}

func newAtlasInstaller(t *testing.T) (*Installer, *fakeReleases, *fakeSignatures) {
	in, rel, sig := newTestInstaller(t)
	in.GOOS, in.GOARCH = runtime.GOOS, runtime.GOARCH
	in.Smoke = RunVersion
	return in, rel, sig
}

func atlasActive(t *testing.T, in *Installer) string {
	t.Helper()
	st, err := in.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if ps, ok := st.Installed("atlas"); ok {
		return ps.Active
	}
	return ""
}

func TestAtlasInstallUpgradeRollbackAndUninstall(t *testing.T) {
	in, rel, sig := newAtlasInstaller(t)
	ctx := context.Background()
	p := publishAtlas(t, rel, "v0.1.0", "v0.1.0")
	rel.latest = "v0.1.0"
	if res, err := in.Install(ctx, p, "", false); err != nil || res.Version != "v0.1.0" {
		t.Fatalf("install: %+v %v", res, err)
	}
	if atlasActive(t, in) != "v0.1.0" || len(sig.identities) != 1 || sig.identities[0] != p.SignerIdentity("v0.1.0") {
		t.Fatal("Atlas version or signature identity mismatch")
	}
	if data, err := os.ReadFile(in.BinaryPath(p, "v0.1.0")); err != nil || !bytes.Equal(data, rel.assets["v0.1.0/atlas-v0.1.0.pyz"]) {
		t.Fatalf("portable package was not preserved: %v", err)
	}
	publishAtlas(t, rel, "v0.2.0", "v0.2.0")
	if _, err := in.Install(ctx, p, "v0.2.0", false); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Install(ctx, p, "v0.1.0", false); err == nil {
		t.Fatal("Atlas downgrade allowed without flag")
	}
	if res, err := in.Rollback(ctx, p); err != nil || res.Version != "v0.1.0" || atlasActive(t, in) != "v0.1.0" {
		t.Fatalf("rollback: %+v %v", res, err)
	}
	if err := in.Uninstall(p); err != nil || atlasActive(t, in) != "" {
		t.Fatalf("uninstall: %v", err)
	}
}

func TestAtlasSignatureRejectedBeforePackageDownload(t *testing.T) {
	in, rel, sig := newAtlasInstaller(t)
	p := publishAtlas(t, rel, "v0.1.0", "v0.1.0")
	sig.err = errors.New("wrong signer")
	if _, err := in.Install(context.Background(), p, "v0.1.0", false); err == nil {
		t.Fatal("invalid signature accepted")
	}
	for _, name := range rel.fetched {
		if strings.HasSuffix(name, ".pyz") {
			t.Fatal("package downloaded before signature verification")
		}
	}
	if atlasActive(t, in) != "" {
		t.Fatal("invalid signature activated")
	}
}

func TestAtlasFailedUpgradeKeepsActiveVersion(t *testing.T) {
	for _, reason := range []string{"checksum", "wrong version", "missing Python"} {
		t.Run(reason, func(t *testing.T) {
			in, rel, _ := newAtlasInstaller(t)
			p := publishAtlas(t, rel, "v0.1.0", "v0.1.0")
			ctx := context.Background()
			if _, err := in.Install(ctx, p, "v0.1.0", false); err != nil {
				t.Fatal(err)
			}
			publishAtlas(t, rel, "v0.2.0", "v0.2.0")
			switch reason {
			case "checksum":
				key := "v0.2.0/atlas-v0.2.0.pyz"
				rel.assets[key] = append(rel.assets[key], 'x')
			case "wrong version":
				publishAtlas(t, rel, "v0.2.0", "v9.0.0")
			case "missing Python":
				t.Setenv("PATH", "")
			}
			if _, err := in.Install(ctx, p, "v0.2.0", false); err == nil {
				t.Fatalf("accepted failed upgrade: %s", reason)
			}
			if atlasActive(t, in) != "v0.1.0" {
				t.Fatal("failed upgrade changed active Atlas")
			}
			if _, err := os.Stat(in.Store.VersionDir("atlas", "v0.2.0")); !os.IsNotExist(err) {
				t.Fatal("failed release left on disk")
			}
		})
	}
}
