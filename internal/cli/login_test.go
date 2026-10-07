package cli

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestUnifiedLoginWithoutInstalledProducts(t *testing.T) {
	t.Setenv("HIVE_TOKEN", "")
	t.Setenv("HIVE_TOKEN_FILE", "")
	t.Setenv("HIVE_CENTER_URL", "")
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	var issued atomic.Value
	center := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/jwks.json" {
			json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "fixture", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
			return
		}
		if r.URL.Path != "/auth/token" {
			t.Error("unexpected endpoint")
			w.WriteHeader(404)
			return
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["audience"] != "hive" || body["username"] != "fixture" || body["password"] != "synthetic" {
			t.Error("login did not request shared audience")
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": issued.Load().(string), "expires_in": 3600})
	}))
	defer center.Close()
	for _, grants := range [][]string{{"product.atlas"}, {"product.mind"}, {"product.mind", "product.atlas"}, {}} {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"aud": "hive", "iss": center.URL, "sub": "00000000-0000-4000-8000-000000000001", "name": "Fixture", "exp": time.Now().Add(time.Hour).Unix(), "permissions": grants})
		token.Header["kid"] = "fixture"
		raw, _ := token.SignedString(key)
		issued.Store(raw)
		app, dispatched, _, stderr := newTestApp(t)
		app.Stdin = strings.NewReader("fixture\nsynthetic\n")
		if code := app.Run([]string{"login", "--center-url", center.URL}); code != 0 {
			t.Fatalf("%d %s", code, stderr.String())
		}
		if dispatched.binary != "" {
			t.Fatal("login depends on product installation")
		}
		path := filepath.Join(app.Store.Root, "token")
		data, _ := os.ReadFile(path)
		if strings.TrimSpace(string(data)) != raw {
			t.Fatal("session differs from issued JWT")
		}
		info, _ := os.Stat(path)
		if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			t.Fatal("token not private")
		}
		if code := app.Run([]string{"login", "--check"}); code != 0 {
			t.Fatalf("check failed: %s", stderr.String())
		}
		if strings.Contains(stderr.String(), raw) {
			t.Fatal("token leaked into output")
		}
		issued.Store(raw + "x")
		app.Stdin = strings.NewReader("fixture\nsynthetic\n")
		if code := app.Run([]string{"login"}); code != 1 {
			t.Fatal("stored invalid signed JWT")
		}
		data, _ = os.ReadFile(path)
		if strings.TrimSpace(string(data)) != raw {
			t.Fatal("failed login overwrote valid session")
		}
		if app.Run([]string{"logout"}) != 0 {
			t.Fatal("logout failed")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("logout left session behind")
		}
	}
}
