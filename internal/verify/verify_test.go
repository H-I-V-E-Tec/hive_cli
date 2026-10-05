package verify

import (
	"os"
	"strings"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/testing/ca"

	"github.com/H-I-V-E-Tec/hive_cli/internal/registry"
)

const releaseSAN = "https://github.com/H-I-V-E-Tec/hive_mind/.github/workflows/release.yml@refs/tags/v1.0.0"

func newVirtual(t *testing.T) (*ca.VirtualSigstore, *Sigstore) {
	t.Helper()
	virtual, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatal(err)
	}
	s, err := newSigstore(virtual, false)
	if err != nil {
		t.Fatal(err)
	}
	return virtual, s
}

func TestProductionVerifierRequiresSCT(t *testing.T) {
	virtual, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatal(err)
	}
	strict, err := NewSigstore(virtual)
	if err != nil {
		t.Fatal(err)
	}
	sums := []byte("abc  hive-v1.0.0-linux-amd64.tar.gz\n")
	entity, err := virtual.Sign(releaseSAN, registry.OIDCIssuer, sums)
	if err != nil {
		t.Fatal(err)
	}
	if err := strict.VerifyEntity(entity, sums, releaseSAN); err == nil || !strings.Contains(err.Error(), "certificate timestamp") {
		t.Fatalf("certificate without SCT must be rejected in production, got %v", err)
	}
}

func TestSigstoreAcceptsExpectedWorkflowIdentity(t *testing.T) {
	virtual, s := newVirtual(t)
	sums := []byte("abc  hive-v1.0.0-linux-amd64.tar.gz\n")
	entity, err := virtual.Sign(releaseSAN, registry.OIDCIssuer, sums)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyEntity(entity, sums, releaseSAN); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
}

func TestSigstoreRejectsOtherIdentities(t *testing.T) {
	sums := []byte("abc  hive-v1.0.0-linux-amd64.tar.gz\n")
	cases := map[string]struct{ san, issuer string }{
		"other repository":  {"https://github.com/attacker/hive_mind/.github/workflows/release.yml@refs/tags/v1.0.0", registry.OIDCIssuer},
		"other workflow":    {"https://github.com/H-I-V-E-Tec/hive_mind/.github/workflows/ci.yml@refs/tags/v1.0.0", registry.OIDCIssuer},
		"other tag":         {"https://github.com/H-I-V-E-Tec/hive_mind/.github/workflows/release.yml@refs/tags/v0.9.0", registry.OIDCIssuer},
		"branch, not a tag": {"https://github.com/H-I-V-E-Tec/hive_mind/.github/workflows/release.yml@refs/heads/main", registry.OIDCIssuer},
		"other issuer":      {releaseSAN, "https://accounts.google.com"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			virtual, s := newVirtual(t)
			entity, err := virtual.Sign(c.san, c.issuer, sums)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.VerifyEntity(entity, sums, releaseSAN); err == nil {
				t.Fatal("signature from unexpected identity accepted")
			}
		})
	}
}

func TestSigstoreRejectsAlteredArtifact(t *testing.T) {
	virtual, s := newVirtual(t)
	sums := []byte("abc  hive-v1.0.0-linux-amd64.tar.gz\n")
	entity, err := virtual.Sign(releaseSAN, registry.OIDCIssuer, sums)
	if err != nil {
		t.Fatal(err)
	}
	tampered := []byte("def  hive-v1.0.0-linux-amd64.tar.gz\n")
	if err := s.VerifyEntity(entity, tampered, releaseSAN); err == nil {
		t.Fatal("signature accepted for altered SHA256SUMS")
	}
}

func TestSigstoreRejectsUntrustedAuthority(t *testing.T) {
	signer, _ := newVirtual(t)
	_, otherTrust := newVirtual(t)
	sums := []byte("abc  hive-v1.0.0-linux-amd64.tar.gz\n")
	entity, err := signer.Sign(releaseSAN, registry.OIDCIssuer, sums)
	if err != nil {
		t.Fatal(err)
	}
	if err := otherTrust.VerifyEntity(entity, sums, releaseSAN); err == nil {
		t.Fatal("signature from an untrusted CA accepted")
	}
}

func TestLegacyCosignBundleIsRejectedClearly(t *testing.T) {
	_, s := newVirtual(t)
	sums, _ := os.ReadFile("testdata/mind-v1.3.10.SHA256SUMS")
	legacy, _ := os.ReadFile("testdata/mind-v1.3.10.legacy-cosign-bundle.json")
	err := s.VerifyBundle(sums, legacy, releaseSAN)
	if err == nil || !strings.Contains(err.Error(), "bundle Sigstore inválido") {
		t.Fatalf("legacy cosign bundle should be rejected as invalid, got %v", err)
	}
}

func TestExpectedSHA256FromRealRelease(t *testing.T) {
	sums, err := os.ReadFile("testdata/mind-v1.3.10.SHA256SUMS")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ExpectedSHA256(sums, "hive-v1.3.10-darwin-arm64.tar.gz")
	if err != nil || got != "f97f23e284f08f548083fd9711440f514bdff721e733050db15970c7ec8638e3" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := ExpectedSHA256(sums, "hive-v1.3.10-darwin-arm64.tar"); err == nil {
		t.Fatal("prefix of an asset name must not match")
	}
}

func TestExpectedSHA256Rejects(t *testing.T) {
	digest := strings.Repeat("a", 64)
	cases := map[string]string{
		"duplicate":  digest + "  x.tar.gz\n" + digest + "  x.tar.gz\n",
		"short hash": "abcd  x.tar.gz\n",
		"not hex":    strings.Repeat("z", 64) + "  x.tar.gz\n",
		"not listed": digest + "  y.tar.gz\n",
	}
	for name, sums := range cases {
		if _, err := ExpectedSHA256([]byte(sums), "x.tar.gz"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := ExpectedSHA256([]byte(digest+" *x.tar.gz\n"), "x.tar.gz"); err != nil {
		t.Errorf("binary-mode entry rejected: %v", err)
	}
}

func TestCheckSHA256(t *testing.T) {
	if err := CheckSHA256([]byte("abc"), "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"); err != nil {
		t.Fatal(err)
	}
	if err := CheckSHA256([]byte("abd"), "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"); err == nil {
		t.Fatal("wrong content accepted")
	}
}
