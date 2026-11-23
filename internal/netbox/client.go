// Package netbox implements a small NetBox REST API client and the inventory
// synchronization logic used by netbox-glean.
package netbox

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Songxwn/netbox-glean/internal/model"
)

// Config configures the NetBox client.
type Config struct {
	URL          string
	Token        string
	TokenVersion string // auto, 1 or 2
	Insecure     bool
	UserAgent    string
}

// Client is a minimal NetBox REST API client.
type Client struct {
	base          string
	authHeader    string
	authMode      string
	hc            *http.Client
	ua            string
	mu            sync.Mutex
	manufacturers map[string]int
}

// TestResult is returned by Test.
type TestResult struct {
	BaseURL      string `json:"base_url"`
	NetboxVersion string `json:"netbox_version"`
	AuthMode     string `json:"auth_mode"`
}

// New creates a client and resolves v1/v2 token authentication.
func New(cfg Config) (*Client, error) {
	base := strings.TrimSpace(cfg.URL)
	if base == "" {
		return nil, fmt.Errorf("NetBox URL 不能为空")
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("无效的 NetBox URL %q", cfg.URL)
	}
	if scheme := strings.ToLower(parsed.Scheme); scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("NetBox URL 仅支持 http/https: %q", cfg.URL)
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	base = strings.TrimRight(parsed.String(), "/")
	authMode, header, err := resolveToken(cfg.Token, cfg.TokenVersion)
	if err != nil {
		return nil, err
	}
	ua := cfg.UserAgent
	if ua == "" {
		ua = "netbox-glean"
	}
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: cfg.Insecure}, // #nosec G402 - explicitly configurable
	}
	return &Client{
		base:          base,
		authHeader:    header,
		authMode:      authMode,
		hc:            &http.Client{Transport: transport, Timeout: 30 * time.Second},
		ua:            ua,
		manufacturers: map[string]int{},
	}, nil
}

// NewFromSettings builds a client from stored settings.
func NewFromSettings(settings model.Settings) (*Client, error) {
	return New(Config{
		URL:          settings.NetboxURL,
		Token:        settings.NetboxToken,
		TokenVersion: settings.TokenVersion,
		Insecure:     settings.NetboxInsecure,
		UserAgent:    "netbox-glean",
	})
}

func resolveToken(token, version string) (string, string, error) {
	token = strings.TrimSpace(token)
	// Accept a pasted "Token ..." or "Bearer ..." value as well as the bare
	// token, so users can copy either form from NetBox documentation.
	if len(token) >= 7 && strings.EqualFold(token[:7], "bearer ") {
		token = strings.TrimSpace(token[7:])
	} else if len(token) >= 6 && strings.EqualFold(token[:6], "token ") {
		token = strings.TrimSpace(token[6:])
	}
	v := strings.ToLower(strings.TrimSpace(version))
	if v == "" {
		v = "auto"
	}
	switch v {
	case "auto":
		if strings.HasPrefix(token, "nbt_") {
			return "2", bearerHeader(token), nil
		}
		return "1", tokenHeader(token), nil
	case "1", "v1", "token":
		return "1", tokenHeader(token), nil
	case "2", "v2", "bearer":
		return "2", bearerHeader(token), nil
	default:
		return "", "", fmt.Errorf("无效的 token_version %q（可选 auto/1/2）", version)
	}
}

func tokenHeader(token string) string {
	if token == "" {
		return ""
	}
	return "Token " + token
}

func bearerHeader(token string) string {
	if token == "" {
		return ""
	}
	return "Bearer " + token
}

// AuthMode reports whether legacy v1 or bearer v2 authentication is used.
func (c *Client) AuthMode() string { return c.authMode }

// BaseURL returns the normalized NetBox base URL.
func (c *Client) BaseURL() string { return c.base }

// Status queries /api/status/ and returns the NetBox version.
func (c *Client) Status(ctx context.Context) (string, error) {
	var status struct {
		NetboxVersion string `json:"netbox-version"`
		DjangoVersion string `json:"django-version"`
	}
	if err := c.get(ctx, "/api/status/", nil, &status); err != nil {
		return "", err
	}
	return status.NetboxVersion, nil
}

// Test verifies connectivity and authentication.
func Test(ctx context.Context, settings model.Settings) (TestResult, error) {
	client, err := NewFromSettings(settings)
	if err != nil {
		return TestResult{}, err
	}
	version, err := client.Status(ctx)
	if err != nil {
		return TestResult{}, err
	}
	// /api/status/ is public on most NetBox installations, so also perform a
	// small authenticated request to validate the token and its permissions.
	if _, err := client.listAll(ctx, "/api/dcim/devices/", url.Values{"limit": {"1"}}, 1); err != nil {
		return TestResult{}, fmt.Errorf("Token 认证或权限检查失败: %w", err)
	}
	return TestResult{
		BaseURL:       client.base,
		NetboxVersion: version,
		AuthMode:      client.authMode,
	}, nil
}

// APIError describes a non-2xx NetBox response.
type APIError struct {
	StatusCode int
	Method     string
	Path       string
	Body       string
}

func (e *APIError) Error() string {
	msg := apiMessage(e.Body)
	if msg == "" {
		return fmt.Sprintf("NetBox API %s %s 返回 HTTP %d", e.Method, e.Path, e.StatusCode)
	}
	return fmt.Sprintf("NetBox API %s %s 返回 HTTP %d: %s", e.Method, e.Path, e.StatusCode, msg)
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	return c.do(ctx, http.MethodGet, pathWithQuery(path, q), nil, out)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("编码请求失败: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.ua)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.authHeader != "" {
		req.Header.Set("Authorization", c.authHeader)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("请求 NetBox 失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("读取 NetBox 响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{
			StatusCode: resp.StatusCode,
			Method:     method,
			Path:       req.URL.Path,
			Body:       string(data),
		}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("解析 NetBox 响应失败: %w", err)
		}
	}
	return nil
}

func pathWithQuery(path string, q url.Values) string {
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}

type listPage struct {
	Count   int               `json:"count"`
	Next    string            `json:"next"`
	Results []json.RawMessage `json:"results"`
}

func (c *Client) listAll(ctx context.Context, path string, q url.Values, max int) ([]json.RawMessage, error) {
	if max <= 0 {
		max = 10000
	}
	next := pathWithQuery(path, q)
	var out []json.RawMessage
	for page := 0; next != "" && page < 1000; page++ {
		var result listPage
		if err := c.get(ctx, next, nil, &result); err != nil {
			return nil, err
		}
		out = append(out, result.Results...)
		if len(out) >= max {
			out = out[:max]
			break
		}
		next = nextRequestPath(result.Next)
	}
	return out, nil
}

func nextRequestPath(next string) string {
	next = strings.TrimSpace(next)
	if next == "" {
		return ""
	}
	u, err := url.Parse(next)
	if err != nil {
		return ""
	}
	if u.IsAbs() {
		return u.RequestURI()
	}
	if strings.HasPrefix(next, "/") {
		return next
	}
	return ""
}

func (c *Client) findID(ctx context.Context, path string, q url.Values) (int, error) {
	results, err := c.listAll(ctx, path, q, 1)
	if err != nil {
		return 0, err
	}
	if len(results) == 0 {
		return 0, nil
	}
	var obj struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(results[0], &obj); err != nil {
		return 0, err
	}
	return obj.ID, nil
}

func apiMessage(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err == nil {
		if detail, ok := payload["detail"].(string); ok && detail != "" {
			return detail
		}
		parts := make([]string, 0, len(payload))
		for key, value := range payload {
			switch v := value.(type) {
			case string:
				parts = append(parts, key+": "+v)
			default:
				encoded, _ := json.Marshal(v)
				parts = append(parts, key+": "+string(encoded))
			}
		}
		if len(parts) > 0 {
			msg := strings.Join(parts, "; ")
			if len(msg) > 500 {
				msg = msg[:500]
			}
			return msg
		}
	}
	if len(body) > 500 {
		body = body[:500]
	}
	return body
}