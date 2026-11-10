package config

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

var errInvalidCORSOrigin = errors.New("invalid browser origin")

func parseCORSOriginList(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	return canonicalCORSOrigins(strings.Split(value, ","))
}

func canonicalCORSOrigins(origins []string) ([]string, error) {
	canonical := make([]string, 0, len(origins))
	seen := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			return nil, errInvalidCORSOrigin
		}
		parsed, err := url.Parse(origin)
		if err != nil || origin == "null" || parsed.User != nil || parsed.Opaque != "" ||
			parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.ForceQuery ||
			parsed.Fragment != "" || parsed.RawFragment != "" ||
			(strings.ToLower(parsed.Scheme) != "http" && strings.ToLower(parsed.Scheme) != "https") || parsed.Hostname() == "" {
			return nil, errInvalidCORSOrigin
		}

		hostname := strings.ToLower(parsed.Hostname())
		for _, char := range hostname {
			if char > 127 || char <= 32 || char == '%' || char == '*' {
				return nil, errInvalidCORSOrigin
			}
		}
		port := parsed.Port()
		if strings.HasSuffix(parsed.Host, ":") {
			return nil, errInvalidCORSOrigin
		}
		if port != "" {
			portNumber, err := strconv.Atoi(port)
			if err != nil || portNumber < 1 || portNumber > 65535 {
				return nil, errInvalidCORSOrigin
			}
			port = strconv.Itoa(portNumber)
		}
		scheme := strings.ToLower(parsed.Scheme)
		if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
			port = ""
		}
		host := hostname
		if strings.Contains(hostname, ":") {
			host = "[" + hostname + "]"
		}
		if port != "" {
			host = net.JoinHostPort(hostname, port)
		}
		value := scheme + "://" + host
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		canonical = append(canonical, value)
	}
	return canonical, nil
}
