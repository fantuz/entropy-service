package lab

import (
	"errors"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// ParseAllowedHosts accepts additional exact DNS names or bare IP addresses.
// These are operator trust decisions; no DNS lookup establishes host trust.
func ParseAllowedHosts(value string) ([]string, error) {
	if value == "" {
		return nil, nil
	}

	parts := strings.Split(value, ",")
	if len(parts) > 32 {
		return nil, errors.New("lab-allowed-hosts accepts at most 32 exact hosts")
	}

	var hosts []string

	for _, part := range parts {
		host, valid := canonicalHost(strings.TrimSpace(part))
		if !valid {
			return nil, errors.New("lab-allowed-hosts requires exact DNS names or bare IPs, without URLs, ports, zones or wildcards")
		}

		if !slices.Contains(hosts, host) {
			hosts = append(hosts, host)
		}
	}

	return hosts, nil
}

// TrustedLocalHost checks the actual HTTP authority, never forwarded headers.
// extra contains the normalized entries returned by ParseAllowedHosts.
func TrustedLocalHost(authority string, extra []string) bool {
	u, err := url.Parse("http://" + authority)
	if err != nil || authority == "" || u.Host != authority || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}

	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
	} else if strings.HasSuffix(authority, ":") {
		return false
	}

	host, valid := canonicalHost(u.Hostname())
	if !valid {
		return false
	}

	if host == "localhost" || slices.Contains(extra, host) {
		return true
	}

	ip, err := netip.ParseAddr(host)

	return err == nil && ip.IsLoopback()
}

func canonicalHost(host string) (string, bool) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Unmap().String(), ip.Zone() == ""
	}

	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" || len(host) > 253 {
		return "", false
	}

	for label := range strings.SplitSeq(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}

		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return "", false
			}
		}
	}

	return host, true
}
