package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type UsernameHistoryEntry struct {
	Username    string `json:"username"`
	ChangedAt   string `json:"changed_at"`
	ScreenName  string `json:"screen_name"`
}

type FrontrunClient struct {
	baseURL     string
	token       string
	clientVer   string
	clientLang  string
	httpClient  *http.Client
}

func NewFrontrunClient(baseURL, token, clientVer, clientLang string) *FrontrunClient {
	return &FrontrunClient{
		baseURL:    baseURL,
		token:      token,
		clientVer:  clientVer,
		clientLang: clientLang,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (fc *FrontrunClient) request(path string) ([]byte, error) {
	u := fmt.Sprintf("%s%s", fc.baseURL, path)
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Cookie", fmt.Sprintf("__Secure-frontrun.session_token=%s; __Secure-frontrun.session_token_domain_migrated=1", fc.token))
	req.Header.Set("X-Copilot-Client-Version", fc.clientVer)
	req.Header.Set("X-Copilot-Client-Language", fc.clientLang)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36")

	resp, err := fc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, u, string(body))
	}

	return body, nil
}

// GetUsernameHistory fetches username history for a handle.
func (fc *FrontrunClient) GetUsernameHistory(handle string) ([]UsernameHistoryEntry, error) {
	path := fmt.Sprintf("/api/v1/twitter/%s/username-history", handle)
	body, err := fc.request(path)
	if err != nil {
		return nil, err
	}

	// Try to parse as array first, then as object with data field
	var entries []UsernameHistoryEntry
	if err := json.Unmarshal(body, &entries); err == nil {
		return entries, nil
	}

	var wrapper struct {
		Data []UsernameHistoryEntry `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		// Try as raw interface
		var raw interface{}
		if err2 := json.Unmarshal(body, &raw); err2 != nil {
			return nil, fmt.Errorf("parse username history: %w", err)
		}
		// If it's an array, try to re-extract
		if arr, ok := raw.([]interface{}); ok {
			for _, item := range arr {
				itemJSON, _ := json.Marshal(item)
				var entry UsernameHistoryEntry
				if json.Unmarshal(itemJSON, &entry) == nil {
					entries = append(entries, entry)
				}
			}
			return entries, nil
		}
		return nil, fmt.Errorf("unexpected response format for username history")
	}
	return wrapper.Data, nil
}

// GetUserInfo fetches user info from Frontrun.
func (fc *FrontrunClient) GetUserInfo(handle string) (map[string]interface{}, error) {
	path := fmt.Sprintf("/api/v3/twitter/%s/info", handle)
	body, err := fc.request(path)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse user info: %w", err)
	}
	return result, nil
}
