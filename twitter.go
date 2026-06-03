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
	"pd8B4kYKjiX3aN5FiKJ7Ug",
	"GiR2kTFyz1GEq6FRiFzNew",
	"T3Et84-subsYoGE45l55SA",
	"6JvzP2Fgf3MY-6GE_AA_6g",
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

	// Path for transaction ID generation
	pathPart := fmt.Sprintf("/i/api/%s", apiPath)
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

// GetUser fetches a user by screen name using Twitter's internal GraphQL API.
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
					Legacy struct {
						ScreenName      string `json:"screen_name"`
						FollowersCount  int    `json:"followers_count"`
						CreatedAt       string `json:"created_at"`
						ProfileImageURL string `json:"profile_image_url_https"`
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

	return &XUser{
		ID:              result.RestID,
		ScreenName:      result.Legacy.ScreenName,
		FollowersCount:  result.Legacy.FollowersCount,
		CreatedAt:       result.Legacy.CreatedAt,
		ProfileImageURL: result.Legacy.ProfileImageURL,
		Name:            result.Legacy.Name,
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

		varsJSON, _ := json.Marshal(variables)
		featuresJSON, _ := json.Marshal(features)

		params := url.Values{}
		params.Set("variables", string(varsJSON))
		params.Set("features", string(featuresJSON))

		var resp struct {
			Data struct {
				User struct {
					Result struct {
						Timeline struct {
							Timeline struct {
								Instructions []struct {
									Type    string `json:"type"`
									Entries []struct {
										EntryID   string `json:"entryId"`
										SortIndex string `json:"sortIndex"`
										Content   struct {
											EntryType   string `json:"entryType"`
											UserResults struct {
												Result struct {
													RestID string `json:"rest_id"`
													Legacy struct {
														ScreenName      string `json:"screen_name"`
														FollowersCount  int    `json:"followers_count"`
														CreatedAt       string `json:"created_at"`
														ProfileImageURL string `json:"profile_image_url_https"`
														Name            string `json:"name"`
													} `json:"legacy"`
												} `json:"result"`
											} `json:"user_results"`
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
		if err := tc.xRequest(apiPath, params, &resp); err != nil {
			// If this hash fails, try the next known hash
			if page == 0 {
				recovered := false
				for _, h := range followersHashes[1:] {
					tc.followersHash = h
					apiPath2 := fmt.Sprintf("%s/Followers", h)
					if err2 := tc.xRequest(apiPath2, params, &resp); err2 == nil {
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
					case entry.Content.UserResults.Result.RestID != "":
						u := entry.Content.UserResults.Result
						allUsers = append(allUsers, XUser{
							ID:              u.RestID,
							ScreenName:      u.Legacy.ScreenName,
							FollowersCount:  u.Legacy.FollowersCount,
							CreatedAt:       u.Legacy.CreatedAt,
							ProfileImageURL: u.Legacy.ProfileImageURL,
							Name:            u.Legacy.Name,
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
