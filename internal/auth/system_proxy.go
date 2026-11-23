package auth

import (
	"net/http"
	"net/url"
)

// ProxyForRequest honors standard proxy environment variables on every
// platform. Windows builds additionally fall back to the user's static
// Internet Settings proxy when no environment proxy is configured.
func ProxyForRequest(req *http.Request) (*url.URL, error) {
	return platformProxyForRequest(req)
}
