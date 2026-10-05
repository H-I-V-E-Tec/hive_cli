package release

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testClient(srv *httptest.Server) *Client {
	return &Client{HTTP: NewHTTPClient(srv.Client().Transport), APIBase: srv.URL, DownloadBase: srv.URL, UserAgent: "test"}
}

func TestLatestParsesTag(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/H-I-V-E-Tec/hive_mind/releases/latest" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"tag_name":"v1.3.10","draft":false}`))
	}))
	defer srv.Close()
	got, err := testClient(srv).Latest(context.Background(), "H-I-V-E-Tec/hive_mind")
	if err != nil || got != "v1.3.10" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestLatestRejectsInvalidTag(t *testing.T) {
	for _, body := range []string{`{"tag_name":"latest"}`, `{"tag_name":"v1.3.10\"; rm -rf /"}`, `not json`} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		if _, err := testClient(srv).Latest(context.Background(), "o/r"); err == nil {
			t.Errorf("accepted %s", body)
		}
		srv.Close()
	}
}

func TestAssetNotFound(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	_, err := testClient(srv).Asset(context.Background(), "o/r", "v1.0.0", "SHA256SUMS", 1024)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestAssetSizeLimit(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("a", 2048)))
	}))
	defer srv.Close()
	if _, err := testClient(srv).Asset(context.Background(), "o/r", "v1.0.0", "x", 1024); err == nil {
		t.Fatal("oversized response accepted")
	}
}

func TestAssetRejectsInvalidVersion(t *testing.T) {
	c := &Client{HTTP: http.DefaultClient, DownloadBase: "https://example.invalid"}
	if _, err := c.Asset(context.Background(), "o/r", "../../x", "y", 10); err == nil {
		t.Fatal("path-like version accepted")
	}
}

func TestRedirectToPlainHTTPRefused(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("payload")) }))
	defer plain.Close()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/payload", http.StatusFound)
	}))
	defer srv.Close()
	_, err := testClient(srv).Asset(context.Background(), "o/r", "v1.0.0", "x", 1024)
	if err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("redirect to http accepted: %v", err)
	}
}

func TestPlainHTTPBaseRefused(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"tag_name":"v1.0.0"}`)) }))
	defer plain.Close()
	c := &Client{HTTP: NewHTTPClient(http.DefaultTransport), APIBase: plain.URL}
	if _, err := c.Latest(context.Background(), "o/r"); err == nil {
		t.Fatal("plain HTTP API accepted")
	}
}
