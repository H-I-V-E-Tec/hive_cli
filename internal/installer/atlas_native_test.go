package installer

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/H-I-V-E-Tec/hive_cli/internal/registry"
	"os"
	"strings"
	"testing"
)

func publishNativeAtlas(t *testing.T, in *Installer, rel *fakeReleases, tag string) registry.Product {
	t.Helper()
	p, _ := registry.Lookup("atlas")
	name := registry.ExecutableName(p.ArchiveBinary, in.GOOS)
	data := []byte("bin:" + tag)
	var archive []byte
	if in.GOOS == "windows" {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		f, _ := z.Create(name)
		_, _ = f.Write(data)
		_ = z.Close()
		archive = b.Bytes()
	} else {
		archive = tarGz(t, map[string][]byte{name: data}, nil)
	}
	asset := p.AssetName(tag, in.GOOS, in.GOARCH)
	sum := sha256.Sum256(archive)
	rel.assets[tag+"/"+asset] = archive
	rel.assets[tag+"/"+SumsName] = []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), asset))
	rel.assets[tag+"/"+BundleName] = []byte(`{"fake":"bundle"}`)
	return p
}
func TestAtlasNativeNeedsNoPython(t *testing.T) {
	in, rel, _ := newTestInstaller(t)
	p := publishNativeAtlas(t, in, rel, "v2.0.0")
	t.Setenv("PATH", "")
	if _, err := in.Install(context.Background(), p, "v2.0.0", false); err != nil {
		t.Fatal(err)
	}
	if got := in.BinaryPath(p, "v2.0.0"); !strings.HasSuffix(got, "hive-atlas") {
		t.Fatal(got)
	}
}
func TestAtlasPythonToGoAndRollback(t *testing.T) {
	in, rel, _ := newAtlasInstaller(t)
	in.Smoke = func(ctx context.Context, binary string, args []string) (string, error) {
		if strings.Contains(binary, "python") || strings.HasSuffix(binary, "py.exe") {
			return RunVersion(ctx, binary, args)
		}
		return fakeSmoke(ctx, binary, args)
	}
	p := publishAtlas(t, rel, "v1.0.0", "v1.0.0")
	if _, err := in.Install(context.Background(), p, "v1.0.0", false); err != nil {
		t.Fatal(err)
	}
	p = publishNativeAtlas(t, in, rel, "v2.0.0")
	if _, err := in.Install(context.Background(), p, "v2.0.0", false); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Rollback(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if atlasActive(t, in) != "v1.0.0" || !strings.HasSuffix(in.BinaryPath(p, "v1.0.0"), ".pyz") {
		t.Fatal("legacy rollback failed")
	}
	if _, err := in.Rollback(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if atlasActive(t, in) != "v2.0.0" {
		t.Fatal("native rollback failed")
	}
}
func TestListedNativeAssetNeverFallsBack(t *testing.T) {
	for _, reason := range []string{"missing", "checksum"} {
		t.Run(reason, func(t *testing.T) {
			in, rel, _ := newTestInstaller(t)
			p := publishNativeAtlas(t, in, rel, "v2.0.0")
			sums := append([]byte{}, rel.assets["v2.0.0/"+SumsName]...)
			publishAtlas(t, rel, "v2.0.0", "v2.0.0")
			rel.assets["v2.0.0/"+SumsName] = append(sums, rel.assets["v2.0.0/"+SumsName]...)
			asset := p.AssetName("v2.0.0", in.GOOS, in.GOARCH)
			if reason == "missing" {
				delete(rel.assets, "v2.0.0/"+asset)
			} else {
				rel.assets["v2.0.0/"+asset] = []byte("corrupt")
			}
			if _, err := in.Install(context.Background(), p, "v2.0.0", false); err == nil {
				t.Fatal("bad native accepted")
			}
			for _, f := range rel.fetched {
				if strings.HasSuffix(f, ".pyz") {
					t.Fatal("fell back after native failure")
				}
			}
			if _, err := os.Stat(in.BinaryPath(p, "v2.0.0")); !os.IsNotExist(err) {
				t.Fatal("failed version installed")
			}
		})
	}
}
