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
	baseURL    string
	httpClient *http.Client
	logger     *zap.Logger
	
	
	runtimesCache     RuntimesResponse
	runtimesCacheOnce sync.Once
	runtimesCacheMu   sync.RWMutex
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
	Language string `json:"language"`
	Version  string `json:"version"`
	Run      Run    `json:"run"`
	Compile Compile `json:"compile,omitempty"`
}


type Run struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Code     int    `json:"code"`
	Signal   string `json:"signal,omitempty"`
	Output   string `json:"output"`
}


type Compile struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	Code   int    `json:"code"`
	Output string `json:"output"`
}


func (c *Client) GetRuntimes(ctx context.Context) (RuntimesResponse, error) {
	
	c.runtimesCacheMu.RLock()
	if c.runtimesCache != nil && len(c.runtimesCache) > 0 {
		cache := c.runtimesCache
		c.runtimesCacheMu.RUnlock()
		return cache, nil
	}
	c.runtimesCacheMu.RUnlock()

	
	httpReq, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/api/v2/runtimes", c.baseURL), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("piston API returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	
	var runtimesArray []Runtime
	if err := json.Unmarshal(body, &runtimesArray); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	
	runtimes := make(RuntimesResponse)
	for _, rt := range runtimesArray {
		runtimes[rt.Language] = append(runtimes[rt.Language], rt)
	}

	
	c.runtimesCacheMu.Lock()
	c.runtimesCache = runtimes
	c.runtimesCacheMu.Unlock()

	return runtimes, nil
}


func (c *Client) Execute(ctx context.Context, req ExecuteRequest) (*ExecuteResponse, error) {
	
	language, version, err := c.mapLanguageToPiston(ctx, req.Language, req.Version)
	if err != nil {
		return nil, fmt.Errorf("failed to map language: %w", err)
	}
	
	
	req.Language = language
	req.Version = version

	
	if req.Timeout == 0 {
		req.Timeout = 10
	}

	
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	
	httpReq, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/api/v2/execute", c.baseURL), bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("piston API returned status %d: %s", resp.StatusCode, string(body))
	}

	
	var executeResp ExecuteResponse
	if err := json.Unmarshal(body, &executeResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &executeResp, nil
}



func (c *Client) mapLanguageToPiston(ctx context.Context, language, requestedVersion string) (string, string, error) {
	
	runtimes, err := c.GetRuntimes(ctx)
	if err != nil {
		c.logger.Warn("Failed to fetch runtimes, using fallback", zap.Error(err))
		
		return c.mapLanguageToPistonFallback(language), c.getDefaultVersionFallback(language, requestedVersion), nil
	}

	
	languageLower := language

	
	if versions, ok := runtimes[languageLower]; ok && len(versions) > 0 {
		version := requestedVersion
		if version == "" {
			
			version = versions[0].Version
		} else {
			
			found := false
			for _, v := range versions {
				if v.Version == version {
					found = true
					break
				}
			}
			if !found {
				
				version = versions[0].Version
			}
		}
		return languageLower, version, nil
	}

	
	for lang, versions := range runtimes {
		for _, v := range versions {
			for _, alias := range v.Aliases {
				if alias == languageLower {
					version := requestedVersion
					if version == "" {
						version = v.Version
					}
					return lang, version, nil
				}
			}
		}
	}

	
	mappedLang := c.mapLanguageToPistonFallback(language)
	if versions, ok := runtimes[mappedLang]; ok && len(versions) > 0 {
		version := requestedVersion
		if version == "" {
			version = versions[0].Version
		}
		return mappedLang, version, nil
	}

	
	version := requestedVersion
	if version == "" {
		version = "*"
	}
	return languageLower, version, nil
}


func (c *Client) mapLanguageToPistonFallback(language string) string {
	languageMap := map[string]string{
		"javascript": "node",
		"cpp":        "gcc",
		"c":          "gcc",
	}
	
	if mapped, ok := languageMap[language]; ok {
		return mapped
	}
	
	return language
}


func (c *Client) getDefaultVersionFallback(language, requestedVersion string) string {
	if requestedVersion != "" {
		return requestedVersion
	}
	
	
	return "*"
}

