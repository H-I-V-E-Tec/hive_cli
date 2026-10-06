// Package registry pins every installable product to its source repository
// and signing identity. This list is the launcher's root of trust: a catalog
// or mirror can never redirect a product to a different origin.
package registry

import "fmt"

// OIDCIssuer is the issuer of the keyless certificates used to sign releases.
const OIDCIssuer = "https://token.actions.githubusercontent.com"

// PythonZipapp is a portable, single-file Python application, not a native binary.
const PythonZipapp = "python-zipapp"

type Product struct {
	Name        string
	Description string
	// Repo is the GitHub repository that publishes the signed releases.
	Repo string
	// AssetPrefix names the archives: <prefix>-<version>-<os>-<arch>.<ext>.
	AssetPrefix string
	// ArchiveBinary is the executable inside the archive, without ".exe".
	ArchiveBinary string
	// Command is the executable name once installed, without ".exe".
	Command string
	// Runtime is empty for native binaries, or PythonZipapp for a .pyz client.
	Runtime string
	// SmokeArgs must print JSON containing {"version": "<tag>"}.
	SmokeArgs []string
}

// SignerIdentity is the certificate SAN the release workflow gets when it runs
// for the given tag; any other signer is rejected.
func (p Product) SignerIdentity(version string) string {
	return fmt.Sprintf("https://github.com/%s/.github/workflows/release.yml@refs/tags/%s", p.Repo, version)
}

func (p Product) AssetName(version, goos, goarch string) string {
	if p.Runtime == PythonZipapp {
		return fmt.Sprintf("%s-%s.pyz", p.AssetPrefix, version)
	}
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("%s-%s-%s-%s.%s", p.AssetPrefix, version, goos, goarch, ext)
}

// InstalledName keeps the portable extension on every OS, including Windows.
func (p Product) InstalledName(goos string) string {
	if p.Runtime == PythonZipapp {
		return p.Command
	}
	return ExecutableName(p.Command, goos)
}

func ExecutableName(base, goos string) string {
	if goos == "windows" {
		return base + ".exe"
	}
	return base
}

// Launcher is the hive CLI itself, updated in place by `hive update`.
var Launcher = Product{
	Name:          "hive",
	Description:   "Launcher HIVE (este comando)",
	Repo:          "H-I-V-E-Tec/hive_cli",
	AssetPrefix:   "hive-cli",
	ArchiveBinary: "hive",
	Command:       "hive",
	SmokeArgs:     []string{"version", "--json"},
}

var products = []Product{
	{
		Name:          "mind",
		Description:   "Hive Mind: memória privada de recon para agentes (MCP)",
		Repo:          "H-I-V-E-Tec/hive_mind",
		AssetPrefix:   "hive",
		ArchiveBinary: "hive",
		Command:       "hive-mind",
		SmokeArgs:     []string{"version"},
	},
	{
		Name:        "atlas",
		Description: "Hive Atlas: biblioteca de sinais e recomendações (MCP; Python 3.10+)",
		Repo:        "H-I-V-E-Tec/hive_atlas",
		AssetPrefix: "atlas",
		Command:     "hive-atlas.pyz",
		Runtime:     PythonZipapp,
		SmokeArgs:   []string{"version", "--json"},
	},
}

func All() []Product {
	out := make([]Product, len(products))
	copy(out, products)
	return out
}

func Lookup(name string) (Product, bool) {
	for _, p := range products {
		if p.Name == name {
			return p, true
		}
	}
	return Product{}, false
}
