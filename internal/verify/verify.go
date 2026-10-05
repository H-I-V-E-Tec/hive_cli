// Package verify checks that release files were produced by the expected
// GitHub workflow (Sigstore keyless signature) and were not altered (SHA-256).
package verify

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	sgverify "github.com/sigstore/sigstore-go/pkg/verify"

	"github.com/H-I-V-E-Tec/hive_cli/internal/registry"
)

// Signatures verifies a Sigstore bundle over an artifact for a certificate SAN.
type Signatures interface {
	VerifyBundle(artifact, bundleJSON []byte, san string) error
}

type Sigstore struct {
	verifier *sgverify.Verifier
}

// NewSigstore requires a Fulcio certificate with an SCT, a Rekor
// transparency-log entry and a log-observed timestamp, as cosign does.
func NewSigstore(trusted root.TrustedMaterial) (*Sigstore, error) {
	return newSigstore(trusted, true)
}

// newSigstore lets tests drop the SCT requirement: sigstore-go's virtual CA
// issues certificates without SCTs. Production always requires them.
func newSigstore(trusted root.TrustedMaterial, requireSCT bool) (*Sigstore, error) {
	opts := []sgverify.VerifierOption{
		sgverify.WithTransparencyLog(1),
		sgverify.WithObserverTimestamps(1),
	}
	if requireSCT {
		opts = append(opts, sgverify.WithSignedCertificateTimestamps(1))
	}
	v, err := sgverify.NewVerifier(trusted, opts...)
	if err != nil {
		return nil, err
	}
	return &Sigstore{verifier: v}, nil
}

// NewPublicGoodSigstore loads the Sigstore public-good trust root through TUF,
// caching its metadata under cacheDir.
func NewPublicGoodSigstore(cacheDir string) (*Sigstore, error) {
	opts := tuf.DefaultOptions()
	opts.CachePath = cacheDir
	trusted, err := root.FetchTrustedRootWithOptions(opts)
	if err != nil {
		return nil, fmt.Errorf("carregar raiz de confiança do Sigstore: %w", err)
	}
	return NewSigstore(trusted)
}

func (s *Sigstore) VerifyBundle(artifact, bundleJSON []byte, san string) error {
	var b bundle.Bundle
	if err := b.UnmarshalJSON(bundleJSON); err != nil {
		return fmt.Errorf("bundle Sigstore inválido: %w", err)
	}
	return s.VerifyEntity(&b, artifact, san)
}

func (s *Sigstore) VerifyEntity(entity sgverify.SignedEntity, artifact []byte, san string) error {
	identity, err := sgverify.NewShortCertificateIdentity(registry.OIDCIssuer, "", san, "")
	if err != nil {
		return err
	}
	policy := sgverify.NewPolicy(sgverify.WithArtifact(bytes.NewReader(artifact)), sgverify.WithCertificateIdentity(identity))
	if _, err := s.verifier.Verify(entity, policy); err != nil {
		return fmt.Errorf("assinatura não confere com %s: %w", san, err)
	}
	return nil
}

// ExpectedSHA256 returns the digest listed for asset in a sha256sum-style file.
func ExpectedSHA256(sums []byte, asset string) (string, error) {
	found := ""
	scanner := bufio.NewScanner(bytes.NewReader(sums))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name != asset {
			continue
		}
		digest := strings.ToLower(fields[0])
		if decoded, err := hex.DecodeString(digest); err != nil || len(decoded) != sha256.Size {
			return "", fmt.Errorf("hash inválido para %s em SHA256SUMS", asset)
		}
		if found != "" {
			return "", fmt.Errorf("%s aparece mais de uma vez em SHA256SUMS", asset)
		}
		found = digest
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("%s não está listado em SHA256SUMS", asset)
	}
	return found, nil
}

func CheckSHA256(data []byte, expected string) error {
	sum := sha256.Sum256(data)
	actual := hex.EncodeToString(sum[:])
	if actual != expected {
		return fmt.Errorf("checksum não confere: esperado %s, obtido %s", expected, actual)
	}
	return nil
}
