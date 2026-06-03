package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	xBaseURL        = "https://x.com/i/api/graphql"
	xBearerToken    = "AAAAAAAAAAAAAAAAAAAAANRILgAAAAAAnNwIzUejRCOuH5E6I8xnZz4puTs%3D1Zv7ttfk8LF81IUq16cHjhLTvJu4FA33AGWWjCpTnA"
	userByScreenHash = "IGgvgiOx4QZndDHuD3x9TQ"
	aboutAccountHash = "zs_jFPFT78rBpXv9Z3U2YQ"
)

// Known Followers endpoint hashes to try
var followersHashes = []string{
	"Wp9x7NPOJ5klmf5H-350gw",
}

type XUser struct {
	ID              string `json:"id"`
	ScreenName      string `json:"screen_name"`
	FollowersCount  int    `json:"followers_count"`
	CreatedAt       string `json:"created_at"`
	ProfileImageURL string `json:"profile_image_url_https"`
	Name            string `json:"name"`
}

type AboutProfile struct {
	Verified    bool   `json:"verified"`
	Description string `json:"description"`
	Followers   int    `json:"followers"`
	Following   int    `json:"following"`
	CreatedAt   string `json:"created_at"`
}

type TwitterClient struct {
	pool       *CookiePool
	httpClient *http.Client
	followersHash string
}

func NewTwitterClient(pool *CookiePool) *TwitterClient {
	return &TwitterClient{
		pool:       pool,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		followersHash: followersHashes[0],
	}
}

func (tc *TwitterClient) xRequest(apiPath string, params url.Values, result interface{}) error {
	cookie := tc.pool.Next()
	u := fmt.Sprintf("%s/%s?%s", xBaseURL, apiPath, params.Encode())

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	// Path for transaction ID generation — must match the full request URL path,
	// including the /graphql segment, per the XClientTransaction reference.
	pathPart := fmt.Sprintf("/i/api/graphql/%s", apiPath)
	transactionID := Generate("GET", pathPart)

	req.Header.Set("authorization", "Bearer "+xBearerToken)
	req.Header.Set("x-twitter-auth-type", "OAuth2Session")
	req.Header.Set("x-twitter-active-user", "yes")
	req.Header.Set("x-csrf-token", cookie.Ct0)
	req.Header.Set("cookie", fmt.Sprintf("auth_token=%s; ct0=%s", cookie.AuthToken, cookie.Ct0))
	req.Header.Set("x-twitter-client-language", "en")
	req.Header.Set("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36")
	if transactionID != "" {
		req.Header.Set("x-client-transaction-id", transactionID)
	}

	resp, err := tc.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		// Try rotating cookies
		if tc.pool.Len() > 1 {
			cookie = tc.pool.Rotate()
			return tc.xRequest(apiPath, params, result)
		}
		return fmt.Errorf("auth error %d: %s", resp.StatusCode, string(body))
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, u, string(body))
	}

	return json.Unmarshal(body, result)
}

// xPostRequest sends a POST request to the Twitter GraphQL API.
func (tc *TwitterClient) xPostRequest(apiPath string, body map[string]interface{}, result interface{}) error {
	cookie := tc.pool.Next()
	u := fmt.Sprintf("%s/%s", xBaseURL, apiPath)

	bodyJSON, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", u, strings.NewReader(string(bodyJSON)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	pathPart := fmt.Sprintf("/i/api/graphql/%s", apiPath)
	transactionID := Generate("POST", pathPart)

	req.Header.Set("authorization", "Bearer "+xBearerToken)
	req.Header.Set("x-twitter-auth-type", "OAuth2Session")
	req.Header.Set("x-twitter-active-user", "yes")
	req.Header.Set("x-csrf-token", cookie.Ct0)
	req.Header.Set("cookie", fmt.Sprintf("auth_token=%s; ct0=%s", cookie.AuthToken, cookie.Ct0))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-twitter-client-language", "en")
	req.Header.Set("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36")
	if transactionID != "" {
		req.Header.Set("x-client-transaction-id", transactionID)
	}

	resp, err := tc.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		if tc.pool.Len() > 1 {
			cookie = tc.pool.Rotate()
			return tc.xPostRequest(apiPath, body, result)
		}
		return fmt.Errorf("auth error %d: %s", resp.StatusCode, string(respBody))
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, u, string(respBody))
	}

	return json.Unmarshal(respBody, result)
}
func (tc *TwitterClient) GetUser(screenName string) (*XUser, error) {
	variables := map[string]interface{}{
		"screen_name":                  screenName,
		"withSafetyModeUserFields":     true,
	}
	features := map[string]interface{}{
		"hidden_profile_subscriptions_enabled":                              true,
		"rweb_tipjar_consumption_enabled":                                  true,
		"responsive_web_graphql_exclude_directive_enabled":                 true,
		"verified_phone_label_enabled":                                     false,
		"subscriptions_verification_info_is_identity_verified_enabled":     true,
		"subscriptions_verification_info_verified_since_enabled":           true,
		"highlights_tweets_tab_ui_enabled":                                 true,
		"responsive_web_twitter_article_notes_tab_enabled":                 true,
		"subscriptions_feature_can_gift_premium":                           true,
		"creator_subscriptions_tweet_preview_api_enabled":                  true,
		"responsive_web_graphql_skip_user_profile_image_extensions_enabled": false,
		"responsive_web_graphql_timeline_navigation_enabled":               true,
	}

	varsJSON, _ := json.Marshal(variables)
	featuresJSON, _ := json.Marshal(features)

	params := url.Values{}
	params.Set("variables", string(varsJSON))
	params.Set("features", string(featuresJSON))

	path := fmt.Sprintf("UserByScreenName/%s", screenName)

	var resp struct {
		Data struct {
			User struct {
				Result struct {
					RestID string `json:"rest_id"`
					Core   struct {
						ScreenName string `json:"screen_name"`
						Name       string `json:"name"`
						CreatedAt  string `json:"created_at"`
					} `json:"core"`
					Legacy struct {
						FollowersCount  int    `json:"followers_count"`
						FriendsCount    int    `json:"friends_count"`
						ProfileImageURL string `json:"profile_image_url_https"`
						CreatedAt       string `json:"created_at"`
						ScreenName      string `json:"screen_name"`
						Name            string `json:"name"`
					} `json:"legacy"`
				} `json:"result"`
			} `json:"user"`
		} `json:"data"`
	}

	_ = path // used for path construction in xRequest

	if err := tc.xRequest(fmt.Sprintf("%s/UserByScreenName", userByScreenHash), params, &resp); err != nil {
		return nil, err
	}

	result := resp.Data.User.Result
	if result.RestID == "" {
		return nil, fmt.Errorf("user @%s not found", screenName)
	}

	// Prefer core fields, fallback to legacy
	sn := result.Core.ScreenName
	if sn == "" {
		sn = result.Legacy.ScreenName
	}
	name := result.Core.Name
	if name == "" {
		name = result.Legacy.Name
	}
	created := result.Core.CreatedAt
	if created == "" {
		created = result.Legacy.CreatedAt
	}
	followers := result.Legacy.FollowersCount
	imgURL := result.Legacy.ProfileImageURL

	return &XUser{
		ID:              result.RestID,
		ScreenName:      sn,
		FollowersCount:  followers,
		CreatedAt:       created,
		ProfileImageURL: imgURL,
		Name:            name,
	}, nil
}

// GetFollowers fetches followers for a user using cursor pagination.
// Returns followers in the order they come (newest first since X returns reverse-chron).
func (tc *TwitterClient) GetFollowers(userID string, maxPages, delayMs int) ([]XUser, error) {
	var allUsers []XUser
	cursor := ""
	page := 0

	for page < maxPages {
		variables := map[string]interface{}{
			"userId":            userID,
			"count":             50,
			"includePromotedContent": false,
		}
		if cursor != "" {
			variables["cursor"] = cursor
		}
		features := map[string]interface{}{
			"rweb_tipjar_consumption_enabled":                                  true,
			"responsive_web_graphql_exclude_directive_enabled":                 true,
			"verified_phone_label_enabled":                                     false,
			"responsive_web_graphql_timeline_navigation_enabled":               true,
			"responsive_web_graphql_skip_user_profile_image_extensions_enabled": false,
			"creator_subscriptions_tweet_preview_api_enabled":                  true,
			"highlights_tweets_tab_ui_enabled":                                 true,
		}

		reqBody := map[string]interface{}{
			"variables": variables,
			"features":  features,
		}

		var resp struct {
			Data struct {
				User struct {
					Result struct {
						Timeline struct {
							Timeline struct {
								Instructions []struct {
									Type    string `json:"type"`
									Entries []struct {
										EntryID string `json:"entryId"`
										Content struct {
											EntryType   string `json:"entryType"`
											ItemContent struct {
												UserResults struct {
													Result struct {
														RestID string `json:"rest_id"`
														Core   struct {
															ScreenName string `json:"screen_name"`
															Name       string `json:"name"`
															CreatedAt  string `json:"created_at"`
														} `json:"core"`
														Legacy struct {
															ScreenName      string `json:"screen_name"`
															FollowersCount  int    `json:"followers_count"`
															CreatedAt       string `json:"created_at"`
															ProfileImageURL string `json:"profile_image_url_https"`
															Name            string `json:"name"`
														} `json:"legacy"`
													} `json:"result"`
												} `json:"user_results"`
											} `json:"itemContent"`
											Value string `json:"value"`
											Token string `json:"token"`
										} `json:"content"`
									} `json:"entries"`
								} `json:"instructions"`
							} `json:"timeline"`
						} `json:"timeline"`
					} `json:"result"`
				} `json:"user"`
			} `json:"data"`
		}

		apiPath := fmt.Sprintf("%s/Followers", tc.followersHash)
		if err := tc.xPostRequest(apiPath, reqBody, &resp); err != nil {
			// Rate limit: wait and retry same page
			if strings.Contains(err.Error(), "429") {
				fmt.Printf("[rate-limit] Page %d hit 429, waiting 60s...\n", page)
				time.Sleep(60 * time.Second)
				continue // retry same page
			}
			// If this hash fails, try the next known hash
			if page == 0 {
				recovered := false
				for _, h := range followersHashes[1:] {
					tc.followersHash = h
					apiPath2 := fmt.Sprintf("%s/Followers", h)
					if err2 := tc.xPostRequest(apiPath2, reqBody, &resp); err2 == nil {
						recovered = true
						break
					}
				}
				if !recovered {
					return allUsers, fmt.Errorf("all Followers endpoint hashes failed: %w", err)
				}
			} else {
				return allUsers, fmt.Errorf("followers page %d: %w", page, err)
			}
		}

		// Parse entries
		foundBottom := false
		var nextCursor string

		for _, inst := range resp.Data.User.Result.Timeline.Timeline.Instructions {
			if inst.Type == "TimelineAddEntries" || inst.Type == "TimelineAddToModule" {
				for _, entry := range inst.Entries {
					switch {
					case strings.HasPrefix(entry.EntryID, "cursor-bottom-"):
						nextCursor = entry.Content.Value
						foundBottom = true
					case strings.HasPrefix(entry.EntryID, "cursor-top-"):
						// skip top cursor
					case entry.Content.ItemContent.UserResults.Result.RestID != "":
						u := entry.Content.ItemContent.UserResults.Result
						sn := u.Core.ScreenName
						if sn == "" {
							sn = u.Legacy.ScreenName
						}
						name := u.Core.Name
						if name == "" {
							name = u.Legacy.Name
						}
						created := u.Core.CreatedAt
						if created == "" {
							created = u.Legacy.CreatedAt
						}
						allUsers = append(allUsers, XUser{
							ID:              u.RestID,
							ScreenName:      sn,
							FollowersCount:  u.Legacy.FollowersCount,
							CreatedAt:       created,
							ProfileImageURL: u.Legacy.ProfileImageURL,
							Name:            name,
						})
					}
				}
			}
		}

		if foundBottom && nextCursor != "" {
			cursor = nextCursor
		} else {
			break
		}

		page++
		if delayMs > 0 && page < maxPages {
			time.Sleep(time.Duration(delayMs) * time.Millisecond)
		}
	}

	return allUsers, nil
}

// GetAboutAccount fetches account info via the AboutAccountQuery.
func (tc *TwitterClient) GetAboutAccount(screenName string) (*AboutProfile, error) {
	variables := map[string]interface{}{
		"screen_name": screenName,
	}
	features := map[string]interface{}{
		"responsive_web_graphql_exclude_directive_enabled":                 true,
		"verified_phone_label_enabled":                                     false,
		"responsive_web_graphql_timeline_navigation_enabled":               true,
		"responsive_web_graphql_skip_user_profile_image_extensions_enabled": false,
	}

	varsJSON, _ := json.Marshal(variables)
	featuresJSON, _ := json.Marshal(features)

	params := url.Values{}
	params.Set("variables", string(varsJSON))
	params.Set("features", string(featuresJSON))

	var resp struct {
		Data struct {
			UserResultByScreenName struct {
				Result struct {
					Legacy struct {
						Description     string `json:"description"`
						FollowersCount  int    `json:"followers_count"`
						FriendsCount    int    `json:"friends_count"`
						CreatedAt       string `json:"created_at"`
					} `json:"legacy"`
					IsVerified    bool `json:"is_verified"`
					Verification  struct {
						Verified bool `json:"verified"`
					} `json:"verification"`
				} `json:"result"`
			} `json:"user_result_by_screen_name"`
		} `json:"data"`
	}

	apiPath := fmt.Sprintf("%s/AboutAccountQuery", aboutAccountHash)
	if err := tc.xRequest(apiPath, params, &resp); err != nil {
		return nil, err
	}

	result := resp.Data.UserResultByScreenName.Result
	return &AboutProfile{
		Verified:    result.IsVerified || result.Verification.Verified,
		Description: result.Legacy.Description,
		Followers:   result.Legacy.FollowersCount,
		Following:   result.Legacy.FriendsCount,
		CreatedAt:   result.Legacy.CreatedAt,
	}, nil
}
