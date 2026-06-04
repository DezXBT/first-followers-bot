package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type UsernameHistoryEntry struct {
	OldUsername string `json:"oldTwitterUsername"`
	ChangedAt   string `json:"changedAt"`
}

type BioHistoryEntry struct {
	Bio         string `json:"bio"`
	LastChecked string `json:"last_checked"`
}

type SmartFollower struct {
	TwitterID           string `json:"twitterId"`
	Name                string `json:"name"`
	Twitter             string `json:"twitter"`
	Bio                 string `json:"bio"`
	ProfilePhoto        string `json:"profilePhoto"`
	FollowersCount      int    `json:"followersCount"`
	SmartFollowersCount int    `json:"smartFollowersCount"`
	FollowedAt          string `json:"followedAt"`
}

type FrontrunClient struct {
	baseURL    string
	token      string
	clientVer  string
	clientLang string
	httpClient *http.Client
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

	var resp struct {
		Data struct {
			UsernameHistory []UsernameHistoryEntry `json:"usernameHistory"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse username history: %w", err)
	}
	if resp.Data.UsernameHistory == nil {
		return []UsernameHistoryEntry{}, nil
	}
	return resp.Data.UsernameHistory, nil
}

// GetBioHistory fetches bio history for a handle.
func (fc *FrontrunClient) GetBioHistory(handle string) ([]BioHistoryEntry, error) {
	path := fmt.Sprintf("/api/v1/twitter/%s/bio-history", handle)
	body, err := fc.request(path)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data struct {
			BioHistory []BioHistoryEntry `json:"bioHistory"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse bio history: %w", err)
	}
	if resp.Data.BioHistory == nil {
		return []BioHistoryEntry{}, nil
	}
	return resp.Data.BioHistory, nil
}

// GetSmartFollowers fetches smart followers for a handle.
func (fc *FrontrunClient) GetSmartFollowers(handle string) ([]SmartFollower, error) {
	path := fmt.Sprintf("/api/v1/twitter/%s/smart-followers", handle)
	body, err := fc.request(path)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data struct {
			SmartFollowers []SmartFollower `json:"smartFollowers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse smart followers: %w", err)
	}
	if resp.Data.SmartFollowers == nil {
		return []SmartFollower{}, nil
	}
	return resp.Data.SmartFollowers, nil
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
