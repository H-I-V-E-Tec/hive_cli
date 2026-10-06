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
	"os"
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
	// Token authenticates GitHub API requests; it is never sent to the CDN.
	Token string
}

func New(userAgent string) *Client {
	token := strings.TrimSpace(os.Getenv("GH_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	}
	return &Client{
		HTTP:         NewHTTPClient(http.DefaultTransport),
		APIBase:      DefaultAPIBase,
		DownloadBase: DefaultDownloadBase,
		UserAgent:    userAgent,
		Token:        token,
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
			// A signed CDN URL does not need the GitHub credential. Strip it on
			// every cross-origin hop, including hosts below the API's domain.
			if len(via) > 0 && !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
				req.Header.Del("Authorization")
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
	if c.Token != "" {
		return c.authenticatedAsset(ctx, repo, version, name, maxBytes)
	}
	u := fmt.Sprintf("%s/%s/releases/download/%s/%s", c.DownloadBase, repo, url.PathEscape(version), url.PathEscape(name))
	data, err := c.get(ctx, u, maxBytes, "application/octet-stream")
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("baixar %s %s: %w (se o repositório for privado, configure GH_TOKEN com Contents: read em %s)", name, version, err, repo)
		}
		return nil, fmt.Errorf("baixar %s %s: %w", name, version, err)
	}
	return data, nil
}

// authenticatedAsset follows the GitHub API download flow for private releases,
// like api-hive-center's server bootstrap. Browser download URLs do not accept
// this API credential for private assets.
func (c *Client) authenticatedAsset(ctx context.Context, repo, version, name string, maxBytes int64) ([]byte, error) {
	base := c.APIBase + "/repos/" + repo
	body, err := c.get(ctx, base+"/releases/tags/"+url.PathEscape(version), maxAPIResponse, "application/vnd.github+json")
	if err != nil {
		return nil, fmt.Errorf("consultar release %s de %s: %w (confira o acesso do token ao repositório)", version, repo, err)
	}
	var metadata struct {
		TagName string `json:"tag_name"`
		Draft   *bool  `json:"draft"`
		Assets  []struct {
			ID    int64  `json:"id"`
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &metadata); err != nil {
		return nil, fmt.Errorf("metadados inválidos da release %s: %w", version, err)
	}
	if metadata.TagName != version || metadata.Draft == nil || *metadata.Draft {
		return nil, fmt.Errorf("metadados inválidos para a release %s", version)
	}
	var id int64
	matches := 0
	for _, asset := range metadata.Assets {
		if asset.Name == name && asset.State == "uploaded" {
			id = asset.ID
			matches++
		}
	}
	if matches == 0 {
		return nil, fmt.Errorf("asset %s na release %s: %w", name, version, ErrNotFound)
	}
	if matches != 1 || id <= 0 {
		return nil, fmt.Errorf("asset %s duplicado ou com ID inválido na release %s", name, version)
	}
	data, err := c.get(ctx, fmt.Sprintf("%s/releases/assets/%d", base, id), maxBytes, "application/octet-stream")
	if err != nil {
		return nil, fmt.Errorf("baixar %s %s pela API: %w", name, version, err)
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
	if c.Token != "" && strings.HasPrefix(rawURL, c.APIBase+"/") {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
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
		return nil, fmt.Errorf("HTTP %d (confira as permissões do token e o limite de requisições do GitHub)", resp.StatusCode)
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
