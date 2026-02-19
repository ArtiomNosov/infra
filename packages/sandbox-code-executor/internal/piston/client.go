package piston

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

type Runtime struct {
	Language string   `json:"language"`
	Version  string   `json:"version"`
	Aliases  []string `json:"aliases"`
}

type RuntimesResponse map[string][]Runtime

type Client struct {
	baseURL       string
	httpClient    *http.Client
	logger        *zap.Logger
	runtimesCache RuntimesResponse
	runtimesOnce  sync.Once
	runtimesMu    sync.RWMutex
}

func NewClient(baseURL string, logger *zap.Logger) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: logger,
	}
}

type ExecuteRequest struct {
	Language string `json:"language"`
	Version  string `json:"version"`
	Files    []File `json:"files"`
	Stdin    string `json:"stdin,omitempty"`
	Timeout  int    `json:"timeout,omitempty"`
}

type File struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type ExecuteResponse struct {
	Language string  `json:"language"`
	Version  string  `json:"version"`
	Run      Run     `json:"run"`
	Compile  Compile `json:"compile,omitempty"`
}

type Run struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	Code   int    `json:"code"`
	Signal string `json:"signal,omitempty"`
	Output string `json:"output"`
}

type Compile struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	Code   int    `json:"code"`
	Output string `json:"output"`
}

func (c *Client) GetRuntimes(ctx context.Context) (RuntimesResponse, error) {
	c.runtimesMu.RLock()
	if c.runtimesCache != nil && len(c.runtimesCache) > 0 {
		cache := c.runtimesCache
		c.runtimesMu.RUnlock()
		return cache, nil
	}
	c.runtimesMu.RUnlock()

	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/api/v2/runtimes", c.baseURL), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("piston API %d: %s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var list []Runtime
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, err
	}
	runtimes := make(RuntimesResponse)
	for _, r := range list {
		runtimes[r.Language] = append(runtimes[r.Language], r)
	}
	c.runtimesMu.Lock()
	c.runtimesCache = runtimes
	c.runtimesMu.Unlock()
	return runtimes, nil
}

func (c *Client) Execute(ctx context.Context, req ExecuteRequest) (*ExecuteResponse, error) {
	lang, version, err := c.mapLanguage(ctx, req.Language, req.Version)
	if err != nil {
		return nil, err
	}
	req.Language = lang
	req.Version = version
	if req.Timeout == 0 {
		req.Timeout = 10
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/api/v2/execute", c.baseURL), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("piston API %d: %s", resp.StatusCode, string(respBody))
	}
	var out ExecuteResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) mapLanguage(ctx context.Context, language, requestedVersion string) (string, string, error) {
	runtimes, err := c.GetRuntimes(ctx)
	if err != nil {
		c.logger.Warn("runtimes fetch failed, using fallback", zap.Error(err))
		return fallbackLanguage(language), "*", nil
	}
	lang := language
	if versions, ok := runtimes[lang]; ok && len(versions) > 0 {
		ver := requestedVersion
		if ver == "" {
			ver = versions[0].Version
		}
		return lang, ver, nil
	}
	for l, versions := range runtimes {
		for _, v := range versions {
			for _, a := range v.Aliases {
				if a == lang {
					ver := requestedVersion
					if ver == "" {
						ver = v.Version
					}
					return l, ver, nil
				}
			}
		}
	}
	mapped := fallbackLanguage(language)
	if versions, ok := runtimes[mapped]; ok && len(versions) > 0 {
		ver := requestedVersion
		if ver == "" {
			ver = versions[0].Version
		}
		return mapped, ver, nil
	}
	return lang, "*", nil
}

func fallbackLanguage(lang string) string {
	m := map[string]string{"javascript": "node", "cpp": "gcc", "c": "gcc"}
	if v, ok := m[lang]; ok {
		return v
	}
	return lang
}
