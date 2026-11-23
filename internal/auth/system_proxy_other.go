//go:build !windows

package auth

import (
	"net/http"
	"net/url"
)

func platformProxyForRequest(req *http.Request) (*url.URL, error) {
	return http.ProxyFromEnvironment(req)
}
