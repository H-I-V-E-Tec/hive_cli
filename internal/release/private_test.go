package release

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const privateMetadata = `{"tag_name":"v1.0.0","draft":false,"assets":[{"name":"atlas-v1.0.0.pyz","state":"uploaded","id":42}]}`

func TestAuthenticatedLatestAndAssetUseAPI(t *testing.T) {
	var assetCalls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Path {
		case "/repos/o/private/releases/latest":
			w.Write([]byte(`{"tag_name":"v1.0.0"}`))
		case "/repos/o/private/releases/tags/v1.0.0":
			if r.Header.Get("Accept") != "application/vnd.github+json" {
				t.Error("release metadata requested without JSON Accept")
			}
			w.Write([]byte(privateMetadata))
		case "/repos/o/private/releases/assets/42":
			assetCalls.Add(1)
			if r.Header.Get("Accept") != "application/octet-stream" {
				t.Error("asset requested without binary Accept")
			}
			w.Write([]byte("verified-later-by-installer"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := testClient(srv)
	c.Token = "test-token"
	if version, err := c.Latest(context.Background(), "o/private"); err != nil || version != "v1.0.0" {
		t.Fatalf("private latest: %s %v", version, err)
	}
	data, err := c.Asset(context.Background(), "o/private", "v1.0.0", "atlas-v1.0.0.pyz", 1024)
	if err != nil || string(data) != "verified-later-by-installer" || assetCalls.Load() != 1 {
		t.Fatalf("private asset: %q %v", data, err)
	}
	c.Token = ""
	if _, err := c.Asset(context.Background(), "o/private", "v1.0.0", "atlas-v1.0.0.pyz", 1024); !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "GH_TOKEN") {
		t.Fatalf("missing private credential not explained: %v", err)
	}
}

func TestAuthenticatedAssetRejectsInvalidMetadata(t *testing.T) {
	for name, body := range map[string]string{
		"wrong tag":     strings.Replace(privateMetadata, `"tag_name":"v1.0.0"`, `"tag_name":"v9.0.0"`, 1),
		"draft":         strings.Replace(privateMetadata, `"draft":false`, `"draft":true`, 1),
		"missing draft": strings.Replace(privateMetadata, `"draft":false,`, "", 1),
		"missing asset": `{"tag_name":"v1.0.0","draft":false,"assets":[]}`,
		"duplicate":     `{"tag_name":"v1.0.0","draft":false,"assets":[{"name":"atlas-v1.0.0.pyz","state":"uploaded","id":42},{"name":"atlas-v1.0.0.pyz","state":"uploaded","id":43}]}`,
		"invalid id":    strings.Replace(privateMetadata, `"id":42`, `"id":-1`, 1),
		"not uploaded":  strings.Replace(privateMetadata, `"state":"uploaded"`, `"state":"starter"`, 1),
		"invalid JSON":  `not JSON`,
	} {
		t.Run(name, func(t *testing.T) {
			var binaryCalls atomic.Int32
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/releases/assets/") {
					binaryCalls.Add(1)
				}
				w.Write([]byte(body))
			}))
			defer srv.Close()
			c := testClient(srv)
			c.Token = "test-token"
			if _, err := c.Asset(context.Background(), "o/private", "v1.0.0", "atlas-v1.0.0.pyz", 1024); err == nil {
				t.Fatal("invalid metadata accepted")
			}
			if binaryCalls.Load() != 0 {
				t.Fatal("binary requested after invalid metadata")
			}
		})
	}
}

func TestPrivateAssetRedirectDoesNotForwardToken(t *testing.T) {
	cdn := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("GitHub token forwarded to CDN")
		}
		w.Write([]byte("payload"))
	}))
	defer cdn.Close()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.NotFound(w, r)
			return
		}
		if strings.Contains(r.URL.Path, "/releases/tags/") {
			w.Write([]byte(privateMetadata))
		} else {
			http.Redirect(w, r, cdn.URL+"/asset", http.StatusFound)
		}
	}))
	defer srv.Close()
	c := testClient(srv)
	c.Token = "test-token"
	if data, err := c.Asset(context.Background(), "o/private", "v1.0.0", "atlas-v1.0.0.pyz", 1024); err != nil || string(data) != "payload" {
		t.Fatalf("CDN redirect: %q %v", data, err)
	}
}

func TestPrivateAssetLimitAlsoAppliesAfterRedirect(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/releases/tags/") {
			w.Write([]byte(privateMetadata))
		} else if strings.Contains(r.URL.Path, "/releases/assets/") {
			http.Redirect(w, r, "/large", http.StatusFound)
		} else {
			w.Write([]byte(strings.Repeat("a", 2048)))
		}
	}))
	defer srv.Close()
	c := testClient(srv)
	c.Token = "test-token"
	if _, err := c.Asset(context.Background(), "o/private", "v1.0.0", "atlas-v1.0.0.pyz", 1024); err == nil || !strings.Contains(err.Error(), "limite") {
		t.Fatalf("oversized private asset accepted: %v", err)
	}
}

func TestTokenEnvironmentPriority(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "fallback-token")
	t.Setenv("GH_TOKEN", " preferred-token\n")
	if New("test").Token != "preferred-token" {
		t.Fatal("GH_TOKEN did not take precedence")
	}
	t.Setenv("GH_TOKEN", "")
	if New("test").Token != "fallback-token" {
		t.Fatal("GITHUB_TOKEN fallback was not used")
	}
}
