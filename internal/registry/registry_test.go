package registry

import "testing"

func TestAtlasPortablePackageAndFixedOrigin(t *testing.T) {
	p, ok := Lookup("atlas")
	if !ok || p.Repo != "H-I-V-E-Tec/hive_atlas" || p.Runtime != PythonZipapp {
		t.Fatalf("Atlas registration: %+v, found=%v", p, ok)
	}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			if got := p.AssetName("v0.2.0", goos, arch); got != "atlas-v0.2.0.pyz" {
				t.Fatalf("%s/%s asset = %s", goos, arch, got)
			}
			if got := p.InstalledName(goos); got != "hive-atlas.pyz" {
				t.Fatalf("%s installed name = %s", goos, got)
			}
		}
	}
	want := "https://github.com/H-I-V-E-Tec/hive_atlas/.github/workflows/release.yml@refs/tags/v0.2.0"
	if got := p.SignerIdentity("v0.2.0"); got != want {
		t.Fatalf("signer = %s", got)
	}
	mind, _ := Lookup("mind")
	if got := mind.InstalledName("windows"); got != "hive-mind.exe" {
		t.Fatalf("Mind native name changed: %s", got)
	}
}
