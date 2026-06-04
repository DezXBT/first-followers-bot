package main

import "sync"

type CookiePair struct {
	AuthToken string
	Ct0       string
}

type CookiePool struct {
	cookies []CookiePair
	cursor  int
	mu      sync.Mutex
}

func NewCookiePool(authTokens, ct0s []string) *CookiePool {
	cookies := make([]CookiePair, len(authTokens))
	for i := range authTokens {
		cookies[i] = CookiePair{
			AuthToken: authTokens[i],
			Ct0:       ct0s[i],
		}
	}
	return &CookiePool{cookies: cookies}
}

// Next returns the next cookie pair in round-robin order.
func (cp *CookiePool) Next() CookiePair {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	cookie := cp.cookies[cp.cursor%len(cp.cookies)]
	cp.cursor++
	return cookie
}

// Len returns the number of cookies in the pool.
func (cp *CookiePool) Len() int {
	return len(cp.cookies)
}
