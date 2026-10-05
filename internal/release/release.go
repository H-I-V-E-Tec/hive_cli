// Package release discovers and downloads GitHub release assets over HTTPS
// with bounded sizes. It performs no trust decisions; see package verify.
package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	DefaultAPIBase      = "https://api.github.com"
	DefaultDownloadBase = "https://github.com"
	maxAPIResponse      = 1 << 20
)

var ErrNotFound = errors.New("não encontrado")

type Client struct {
	HTTP         *http.Client
	APIBase      string
	DownloadBase string
	UserAgent    string
}

func New(userAgent string) *Client {
	return &Client{
		HTTP:         NewHTTPClient(http.DefaultTransport),
		APIBase:      DefaultAPIBase,
		DownloadBase: DefaultDownloadBase,
		UserAgent:    userAgent,
	}
}

// NewHTTPClient refuses redirects that leave HTTPS (GitHub redirects asset
// downloads to its CDN, which is fine as long as it stays on TLS).
func NewHTTPClient(rt http.RoundTripper) *http.Client {
	return &http.Client{
		Transport: rt,
		Timeout:   10 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("redirecionamentos demais")
			}
			if req.URL.Scheme != "https" {
				return fmt.Errorf("redirecionamento recusado para URL sem HTTPS: %s", req.URL.Redacted())
			}
			return nil
		},
	}
}

// Latest returns the tag of the repository's latest published (non-draft,
// non-prerelease) release, as reported by the GitHub API.
func (c *Client) Latest(ctx context.Context, repo string) (string, error) {
	body, err := c.get(ctx, c.APIBase+"/repos/"+repo+"/releases/latest", maxAPIResponse, "application/vnd.github+json")
	if err != nil {
		return "", fmt.Errorf("consultar última release de %s: %w", repo, err)
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("resposta inválida da API do GitHub: %w", err)
	}
	if !semver.IsValid(payload.TagName) {
		return "", fmt.Errorf("tag de release inválida em %s: %q", repo, payload.TagName)
	}
	return payload.TagName, nil
}

func (c *Client) Asset(ctx context.Context, repo, version, name string, maxBytes int64) ([]byte, error) {
	if !semver.IsValid(version) {
		return nil, fmt.Errorf("versão inválida: %q", version)
	}
	u := fmt.Sprintf("%s/%s/releases/download/%s/%s", c.DownloadBase, repo, url.PathEscape(version), url.PathEscape(name))
	data, err := c.get(ctx, u, maxBytes, "application/octet-stream")
	if err != nil {
		return nil, fmt.Errorf("baixar %s %s: %w", name, version, err)
	}
	return data, nil
}

func (c *Client) get(ctx context.Context, rawURL string, maxBytes int64, accept string) ([]byte, error) {
	if !strings.HasPrefix(rawURL, "https://") {
		return nil, fmt.Errorf("URL sem HTTPS recusada: %s", rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("HTTP %d (limite de requisições do GitHub? tente novamente mais tarde)", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("resposta maior que o limite de %d bytes", maxBytes)
	}
	return data, nil
}
