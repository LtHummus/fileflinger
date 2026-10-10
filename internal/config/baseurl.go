package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// CheckBaseURL looks for problems with base_url, which share links are built
// from. An error means links can't work at all; the warnings are lesser problems.
func CheckBaseURL(baseURL string) ([]string, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("base_url is not set, so share links will not work")
	}

	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("base_url %q is not an http(s) URL, so share links will not work", baseURL)
	}

	var warnings []string
	if u.RawQuery != "" || u.Fragment != "" {
		warnings = append(warnings, "base_url has a query or fragment, which will end up in the middle of share links")
	}
	if u.Scheme == "http" && !IsLocalhostOrPrivate(u.Hostname()) {
		warnings = append(warnings, "base_url uses http, so share links and downloads are not encrypted")
	}
	return warnings, nil
}

func IsLocalhostOrPrivate(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
}
