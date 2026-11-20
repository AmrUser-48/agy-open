package openai

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// chatGPTInfraCookieJar retains only ChatGPT infrastructure cookies used for
// Cloudflare challenges and backend routing. It must never store account,
// session, or authentication cookies.
type chatGPTInfraCookieJar struct {
	mu      sync.Mutex
	cookies map[string]storedChatGPTCookie
}

type storedChatGPTCookie struct {
	cookie   http.Cookie
	hostOnly bool
	expires  time.Time
}

func newChatGPTInfraCookieJar() *chatGPTInfraCookieJar {
	return &chatGPTInfraCookieJar{cookies: make(map[string]storedChatGPTCookie)}
}

func (j *chatGPTInfraCookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	if j == nil || !allowedChatGPTCookieURL(u) {
		return
	}
	host := strings.ToLower(u.Hostname())
	now := time.Now()

	j.mu.Lock()
	defer j.mu.Unlock()

	for _, cookie := range cookies {
		if cookie == nil || !allowedChatGPTInfrastructureCookie(cookie.Name) {
			continue
		}

		domain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(cookie.Domain)), ".")
		hostOnly := domain == ""
		if hostOnly {
			domain = host
		}
		if !allowedChatGPTCookieDomain(domain) || !cookieDomainMatches(host, domain) {
			continue
		}

		path := cookie.Path
		if path == "" || !strings.HasPrefix(path, "/") {
			path = "/"
		}
		key := domain + "\x00" + path + "\x00" + cookie.Name

		if cookie.MaxAge < 0 || (!cookie.Expires.IsZero() && !cookie.Expires.After(now)) {
			delete(j.cookies, key)
			continue
		}

		copyCookie := *cookie
		copyCookie.Domain = domain
		copyCookie.Path = path
		expires := cookie.Expires
		if cookie.MaxAge > 0 {
			expires = now.Add(time.Duration(cookie.MaxAge) * time.Second)
		}
		j.cookies[key] = storedChatGPTCookie{
			cookie:   copyCookie,
			hostOnly: hostOnly,
			expires:  expires,
		}
	}
}

func (j *chatGPTInfraCookieJar) Cookies(u *url.URL) []*http.Cookie {
	if j == nil || !allowedChatGPTCookieURL(u) {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	requestPath := u.EscapedPath()
	if requestPath == "" {
		requestPath = "/"
	}
	now := time.Now()

	j.mu.Lock()
	defer j.mu.Unlock()

	result := make([]*http.Cookie, 0, len(j.cookies))
	for key, stored := range j.cookies {
		cookie := &stored.cookie
		if !stored.expires.IsZero() && !stored.expires.After(now) {
			delete(j.cookies, key)
			continue
		}
		if cookie.Secure && u.Scheme != "https" {
			continue
		}
		if stored.hostOnly {
			if host != cookie.Domain {
				continue
			}
		} else if !cookieDomainMatches(host, cookie.Domain) {
			continue
		}
		if !cookiePathMatches(requestPath, cookie.Path) {
			continue
		}

		copyCookie := *cookie
		copyCookie.Raw = ""
		copyCookie.Unparsed = nil
		copyCookie.MaxAge = 0
		result = append(result, &copyCookie)
	}
	return result
}

func allowedChatGPTCookieURL(u *url.URL) bool {
	if u == nil || u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "chatgpt.com" ||
		strings.HasSuffix(host, ".chatgpt.com") ||
		host == "chat.openai.com" ||
		host == "chatgpt-staging.com" ||
		strings.HasSuffix(host, ".chatgpt-staging.com")
}

func allowedChatGPTCookieDomain(domain string) bool {
	return domain == "chatgpt.com" ||
		strings.HasSuffix(domain, ".chatgpt.com") ||
		domain == "chat.openai.com" ||
		domain == "chatgpt-staging.com" ||
		strings.HasSuffix(domain, ".chatgpt-staging.com")
}

func cookieDomainMatches(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func cookiePathMatches(requestPath, cookiePath string) bool {
	if requestPath == cookiePath {
		return true
	}
	if !strings.HasPrefix(requestPath, cookiePath) {
		return false
	}
	return strings.HasSuffix(cookiePath, "/") ||
		(len(requestPath) > len(cookiePath) && requestPath[len(cookiePath)] == '/')
}

func allowedChatGPTInfrastructureCookie(name string) bool {
	switch name {
	case "__cf_bm", "__cflb", "__cfruid", "__cfseq", "__cfwaitingroom",
		"__oailb", "_cfuvid", "cf_clearance", "cf_ob_info", "cf_use_ob":
		return true
	default:
		return strings.HasPrefix(name, "cf_chl_")
	}
}
