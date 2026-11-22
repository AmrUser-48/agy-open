//go:build windows

package auth

import (
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const internetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

func platformProxyForRequest(req *http.Request) (*url.URL, error) {
	proxy, err := http.ProxyFromEnvironment(req)
	if err != nil || proxy != nil {
		return proxy, err
	}
	// Respect an explicit no-proxy policy rather than overriding it with the
	// per-user Windows proxy configuration.
	if os.Getenv("NO_PROXY") != "" || os.Getenv("no_proxy") != "" {
		return nil, nil
	}
	return windowsInternetProxy(req)
}

func windowsInternetProxy(req *http.Request) (*url.URL, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKey, registry.QUERY_VALUE)
	if err != nil {
		return nil, nil
	}
	defer key.Close()

	enabled, _, err := key.GetIntegerValue("ProxyEnable")
	if err != nil || enabled == 0 {
		return nil, nil
	}
	server, _, err := key.GetStringValue("ProxyServer")
	if err != nil || strings.TrimSpace(server) == "" {
		return nil, nil
	}
	bypass, _, _ := key.GetStringValue("ProxyOverride")
	if windowsProxyBypass(req.URL.Hostname(), bypass) {
		return nil, nil
	}

	address := windowsProxyAddress(server, req.URL.Scheme)
	if address == "" {
		return nil, nil
	}
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	proxy, err := url.Parse(address)
	if err != nil || proxy.Host == "" {
		return nil, nil
	}
	return proxy, nil
}

func windowsProxyAddress(server, scheme string) string {
	fallback := ""
	for _, entry := range strings.Split(server, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if key, value, ok := strings.Cut(entry, "="); ok {
			value = strings.TrimSpace(value)
			if strings.EqualFold(strings.TrimSpace(key), scheme) {
				return value
			}
			if fallback == "" {
				fallback = value
			}
			continue
		}
		if fallback == "" {
			fallback = entry
		}
	}
	return fallback
}

func windowsProxyBypass(host, list string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	for _, entry := range strings.Split(list, ";") {
		rule := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(entry)), ".")
		if rule == "" {
			continue
		}
		if rule == "<local>" && !strings.Contains(host, ".") {
			return true
		}
		rule = strings.TrimPrefix(rule, "*.")
		if strings.HasPrefix(rule, ".") {
			if host == strings.TrimPrefix(rule, ".") || strings.HasSuffix(host, rule) {
				return true
			}
			continue
		}
		if host == rule || strings.HasSuffix(host, "."+rule) {
			return true
		}
	}
	return false
}
