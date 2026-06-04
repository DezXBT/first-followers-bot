package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	xBaseURL         = "https://x.com/i/api/graphql"
	xBearerToken     = "AAAAAAAAAAAAAAAAAAAAANRILgAAAAAAnNwIzUejRCOuH5E6I8xnZz4puTs%3D1Zv7ttfk8LF81IUq16cHjhLTvJu4FA33AGWWjCpTnA"
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
	IsBlueVerified  bool   `json:"is_blue_verified"`
	Followers       int    `json:"followers"`
	CreatedAt       string `json:"created_at"`
	AccountBasedIn  string `json:"account_based_in"`
	Source          string `json:"source"`
	UsernameChanges int    `json:"username_changes"`
	AvatarURL       string `json:"avatar_url"`
	Name            string `json:"name"`
}

type TwitterClient struct {
	pool       *CookiePool
	httpClient *http.Client

	mu            sync.Mutex // guards followersHash (shared across concurrent .first crawls)
	followersHash string
}

func NewTwitterClient(pool *CookiePool) *TwitterClient {
	return &TwitterClient{
		pool:          pool,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		followersHash: followersHashes[0],
	}
}

func (tc *TwitterClient) getFollowersHash() string {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.followersHash
}

func (tc *TwitterClient) setFollowersHash(h string) {
	tc.mu.Lock()
	tc.followersHash = h
	tc.mu.Unlock()
}

// authAttempts returns how many times an auth-failing request should be retried:
// once per cookie in the pool, so a fully-expired pool fails fast instead of looping forever.
func (tc *TwitterClient) authAttempts() int {
	if n := tc.pool.Len(); n > 1 {
		return n
	}
	return 1
}

func (tc *TwitterClient) xRequest(apiPath string, params url.Values, result interface{}) error {
	u := fmt.Sprintf("%s/%s?%s", xBaseURL, apiPath, params.Encode())
	pathPart := fmt.Sprintf("/i/api/%s", apiPath)

	var lastErr error
	for attempt := 0; attempt < tc.authAttempts(); attempt++ {
		cookie := tc.pool.Next()

		req, err := http.NewRequest("GET", u, nil)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}

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
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("read body: %w", err)
		}

		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			// Auth failed: rotate to the next cookie and retry, bounded by authAttempts.
			lastErr = fmt.Errorf("auth error %d: %s", resp.StatusCode, string(body))
			continue
		}
		if resp.StatusCode != 200 {
			return fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, u, string(body))
		}
		return json.Unmarshal(body, result)
	}
	return lastErr
}

// xPostRequest sends a POST request to the Twitter GraphQL API.
func (tc *TwitterClient) xPostRequest(apiPath string, body map[string]interface{}, result interface{}) error {
	u := fmt.Sprintf("%s/%s", xBaseURL, apiPath)
	pathPart := fmt.Sprintf("/i/api/%s", apiPath)
	bodyJSON, _ := json.Marshal(body)

	var lastErr error
	for attempt := 0; attempt < tc.authAttempts(); attempt++ {
		cookie := tc.pool.Next()

		req, err := http.NewRequest("POST", u, strings.NewReader(string(bodyJSON)))
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}

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
		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("read body: %w", err)
		}

		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			lastErr = fmt.Errorf("auth error %d: %s", resp.StatusCode, string(respBody))
			continue
		}
		if resp.StatusCode != 200 {
			return fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, u, string(respBody))
		}
		return json.Unmarshal(respBody, result)
	}
	return lastErr
}
func (tc *TwitterClient) GetUser(screenName string) (*XUser, error) {
	variables := map[string]interface{}{
		"screen_name":              screenName,
		"withSafetyModeUserFields": true,
	}
	features := map[string]interface{}{
		"rweb_tipjar_consumption_enabled":                                         true,
		"responsive_web_graphql_exclude_directive_enabled":                        true,
		"verified_phone_label_enabled":                                            false,
		"creator_subscriptions_tweet_preview_api_enabled":                         true,
		"responsive_web_graphql_timeline_navigation_enabled":                      true,
		"responsive_web_graphql_skip_user_profile_image_extensions_enabled":       false,
		"communities_web_enable_tweet_community_results_fetch":                    true,
		"c9s_tweet_anatomy_moderator_badge_enabled":                               true,
		"articles_preview_enabled":                                                true,
		"tweetypie_unmention_optimization_enabled":                                true,
		"responsive_web_edit_tweet_api_enabled":                                   true,
		"graphql_is_translatable_rweb_tweet_is_translatable_enabled":              true,
		"view_counts_everywhere_api_enabled":                                      true,
		"longform_notetweets_consumption_enabled":                                 true,
		"responsive_web_twitter_article_tweet_consumption_enabled":                true,
		"tweet_awards_web_tipping_enabled":                                        false,
		"creator_subscriptions_quote_tweet_preview_enabled":                       false,
		"freedom_of_speech_not_reach_fetch_enabled":                               true,
		"standardized_nudges_misinfo":                                             true,
		"tweet_with_visibility_results_prefer_gql_limited_actions_policy_enabled": true,
		"rweb_video_timestamps_enabled":                                           true,
		"longform_notetweets_rich_text_read_enabled":                              true,
		"longform_notetweets_inline_media_enabled":                                true,
		"responsive_web_enhance_cards_enabled":                                    false,
		"responsive_web_twitter_article_notes_tab_enabled":                        true,
		"subscriptions_verification_info_verified_since_enabled":                  true,
		"subscriptions_verification_info_is_identity_verified_enabled":            true,
		"highlights_tweets_tab_ui_enabled":                                        true,
		"profile_label_improvements_pcf_label_in_post_enabled":                    true,
		"hidden_profile_subscriptions_enabled":                                    true,
		"subscriptions_feature_can_gift_premium":                                  true,
		"responsive_web_grok_show_grok_translated_post":                           true,
		"responsive_web_grok_analyze_post_followups_enabled":                      true,
		"premium_content_api_read_enabled":                                        true,
		"responsive_web_grok_image_annotation_enabled":                            true,
		"responsive_web_grok_share_attachment_enabled":                            true,
		"responsive_web_grok_analysis_button_from_backend":                        true,
		"responsive_web_grok_analyze_button_fetch_trends_enabled":                 true,
		"rweb_video_screen_enabled":                                               true,
		"responsive_web_jetfuel_frame":                                            true,
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
					RestID string `json:"rest_id"`
					Core   struct {
						ScreenName string `json:"screen_name"`
						Name       string `json:"name"`
						CreatedAt  string `json:"created_at"`
					} `json:"core"`
					Avatar struct {
						ImageURL string `json:"image_url"`
					} `json:"avatar"`
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

	// Profile image: new API uses avatar.image_url instead of legacy.profile_image_url_https
	imgURL := result.Avatar.ImageURL
	if imgURL == "" {
		imgURL = result.Legacy.ProfileImageURL // fallback
	}

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
	const maxRateLimitRetries = 5

	var allUsers []XUser
	cursor := ""
	page := 0
	rateLimitRetries := 0

	fmt.Printf("[followers] Starting crawl: userID=%s, maxPages=%d, delay=%dms\n", userID, maxPages, delayMs)

	for page < maxPages {
		variables := map[string]interface{}{
			"userId":                 userID,
			"count":                  50,
			"includePromotedContent": false,
		}
		if cursor != "" {
			variables["cursor"] = cursor
		}
		features := map[string]interface{}{
			"rweb_tipjar_consumption_enabled":                                   true,
			"responsive_web_graphql_exclude_directive_enabled":                  true,
			"verified_phone_label_enabled":                                      false,
			"responsive_web_graphql_timeline_navigation_enabled":                true,
			"responsive_web_graphql_skip_user_profile_image_extensions_enabled": false,
			"creator_subscriptions_tweet_preview_api_enabled":                   true,
			"highlights_tweets_tab_ui_enabled":                                  true,
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
														Avatar struct {
															ImageURL string `json:"image_url"`
														} `json:"avatar"`
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

		apiPath := fmt.Sprintf("%s/Followers", tc.getFollowersHash())
		if err := tc.xPostRequest(apiPath, reqBody, &resp); err != nil {
			// Rate limit: wait and retry the same page, but give up after a bounded number of
			// attempts so a persistently throttled account can't hang the crawl forever.
			if strings.Contains(err.Error(), "429") {
				rateLimitRetries++
				if rateLimitRetries > maxRateLimitRetries {
					return allUsers, fmt.Errorf("followers page %d: rate-limited after %d retries: %w", page, maxRateLimitRetries, err)
				}
				fmt.Printf("[rate-limit] Page %d hit 429 (retry %d/%d), waiting 60s...\n", page, rateLimitRetries, maxRateLimitRetries)
				time.Sleep(60 * time.Second)
				continue // retry same page
			}
			// If this hash fails, try the next known hash
			if page == 0 {
				recovered := false
				for _, h := range followersHashes[1:] {
					tc.setFollowersHash(h)
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
		} else {
			// Successful page resets the rate-limit budget.
			rateLimitRetries = 0
		}

		// Parse entries
		foundBottom := false
		var nextCursor string
		usersThisPage := 0

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
							ProfileImageURL: u.Avatar.ImageURL,
							Name:            name,
						})
						usersThisPage++
					}
				}
			}
		}

		// End of the reachable list: X keeps returning a bottom cursor even on empty pages, which
		// would otherwise loop until maxPages. Stop as soon as a page yields no new followers.
		if usersThisPage == 0 {
			fmt.Printf("[followers] Page %d returned no new users, stopping (collected %d)\n", page, len(allUsers))
			break
		}

		if foundBottom && nextCursor != "" {
			cursor = nextCursor
		} else {
			break
		}

		page++

		if page%5 == 0 || page >= maxPages {
			fmt.Printf("[followers] Page %d/%d done, collected %d users so far\n", page, maxPages, len(allUsers))
		}

		if delayMs > 0 && page < maxPages {
			time.Sleep(time.Duration(delayMs) * time.Millisecond)
		}
	}

	return allUsers, nil
}

// GetAboutAccount fetches account info via the AboutAccountQuery.
func (tc *TwitterClient) GetAboutAccount(screenName string) (*AboutProfile, error) {
	variables := map[string]interface{}{
		"screenName": screenName,
	}
	features := map[string]interface{}{
		"responsive_web_graphql_exclude_directive_enabled":                  true,
		"verified_phone_label_enabled":                                      false,
		"responsive_web_graphql_timeline_navigation_enabled":                true,
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
					AboutProfile struct {
						AccountBasedIn  string `json:"account_based_in"`
						Source          string `json:"source"`
						UsernameChanges struct {
							Count string `json:"count"`
						} `json:"username_changes"`
					} `json:"about_profile"`
					Core struct {
						CreatedAt  string `json:"created_at"`
						Name       string `json:"name"`
						ScreenName string `json:"screen_name"`
					} `json:"core"`
					Avatar struct {
						ImageURL string `json:"image_url"`
					} `json:"avatar"`
					IsBlueVerified bool `json:"is_blue_verified"`
				} `json:"result"`
			} `json:"user_result_by_screen_name"`
		} `json:"data"`
	}

	apiPath := fmt.Sprintf("%s/AboutAccountQuery", aboutAccountHash)
	if err := tc.xRequest(apiPath, params, &resp); err != nil {
		return nil, err
	}

	result := resp.Data.UserResultByScreenName.Result

	// Parse username_changes count (string from API, convert to int)
	ucCount := 0
	if n, err := fmt.Sscanf(result.AboutProfile.UsernameChanges.Count, "%d", &ucCount); err != nil || n == 0 {
		ucCount = 0
	}

	return &AboutProfile{
		IsBlueVerified:  result.IsBlueVerified,
		CreatedAt:       result.Core.CreatedAt,
		AccountBasedIn:  result.AboutProfile.AccountBasedIn,
		Source:          result.AboutProfile.Source,
		UsernameChanges: ucCount,
		AvatarURL:       result.Avatar.ImageURL,
		Name:            result.Core.Name,
	}, nil
}
