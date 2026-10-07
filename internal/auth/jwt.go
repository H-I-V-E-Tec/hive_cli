package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	jwt.RegisteredClaims
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

type Authorizer struct {
	Center      string
	Client      *http.Client
	mu          sync.Mutex
	keys        map[string]*rsa.PublicKey
	fetched     time.Time
	lastRefresh time.Time
}

func NewAuthorizer(center string) (*Authorizer, error) {
	center, err := ValidateURL(center)
	if err != nil {
		return nil, err
	}
	client, err := HTTPClient("")
	if err != nil {
		return nil, err
	}
	client.Timeout = 10 * time.Second
	return &Authorizer{Center: center, Client: client, keys: map[string]*rsa.PublicKey{}}, nil
}
func (a *Authorizer) Validate(ctx context.Context, raw string) (*Claims, error) {
	if len(raw) > 16384 {
		return nil, fmt.Errorf("invalid token")
	}
	claims := &Claims{}
	tok, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" || len(kid) > 256 {
			return nil, fmt.Errorf("missing kid")
		}
		return a.key(ctx, kid)
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithExpirationRequired(), jwt.WithIssuer(a.Center), jwt.WithAudience("hive"))
	if err != nil || !tok.Valid || !validSubject(claims.Subject) {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}
func validSubject(sub string) bool {
	if len(sub) != 36 {
		return false
	}
	for i, c := range sub {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}
func (a *Authorizer) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if key := a.keys[kid]; key != nil && time.Since(a.fetched) < 5*time.Minute {
		return key, nil
	}
	// Unknown kids trigger a refresh for rotation, bounded to avoid an attacker
	// turning invalid JWT headers into unlimited Center requests.
	if time.Since(a.lastRefresh) < time.Second {
		return nil, fmt.Errorf("unknown signing key")
	}
	a.lastRefresh = time.Now()
	var doc struct {
		Keys []struct{ Kty, Kid, Alg, Use, N, E string } `json:"keys"`
	}
	if err := JSON(ctx, a.Client, "GET", a.Center+"/.well-known/jwks.json", "", nil, &doc); err != nil {
		return nil, err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		// alg/use are optional JWK metadata; the Center currently omits them.
		// JWT parsing still pins RS256 and rejects contradictory metadata.
		if k.Kty != "RSA" || k.Kid == "" || k.Alg != "" && k.Alg != "RS256" || k.Use != "" && k.Use != "sig" {
			continue
		}
		n, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		e, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil || len(e) > 4 {
			continue
		}
		exp := new(big.Int).SetBytes(e).Int64()
		modulus := new(big.Int).SetBytes(n)
		if exp < 3 || exp%2 == 0 || modulus.BitLen() < 2048 {
			continue
		}
		if _, duplicate := keys[k.Kid]; duplicate {
			return nil, fmt.Errorf("duplicate signing key")
		}
		keys[k.Kid] = &rsa.PublicKey{N: modulus, E: int(exp)}
	}
	a.keys = keys
	a.fetched = time.Now()
	if key := keys[kid]; key != nil {
		return key, nil
	}
	return nil, fmt.Errorf("unknown signing key")
}
