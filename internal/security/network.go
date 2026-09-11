package security

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
	"time"
)

var (
	ErrInvalidScheme = errors.New("unsupported URL scheme: only http and https are allowed")
	ErrBlockedHost   = errors.New("host resolution blocked by SSRF policy")
	ErrEmptyHost     = errors.New("URL host cannot be empty")
)

// ValidateURLScheme checks that the URL uses http or https.
func ValidateURLScheme(rawURL string) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("%w: %q", ErrInvalidScheme, u.Scheme)
	}

	if strings.TrimSpace(u.Host) == "" {
		return nil, ErrEmptyHost
	}

	return u, nil
}

// IsPrivateOrLoopbackIP checks if an IP belongs to private/loopback/link-local ranges.
func IsPrivateOrLoopbackIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() {
		return true
	}
	// Check IPv4-mapped IPv6 addresses (e.g. ::ffff:127.0.0.1)
	if ip4 := ip.To4(); ip4 != nil {
		return ip4.IsLoopback() || ip4.IsLinkLocalUnicast() || ip4.IsLinkLocalMulticast() || ip4.IsPrivate()
	}
	return false
}

// ValidatePublicTarget resolves the host and verifies that it does not resolve to private or loopback addresses.
func ValidatePublicTarget(hostname string) error {
	host := hostname
	if h, _, err := net.SplitHostPort(hostname); err == nil {
		host = h
	}

	// Remove IPv6 brackets if present
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")

	// If it's a literal IP
	if ip := net.ParseIP(host); ip != nil {
		if IsPrivateOrLoopbackIP(ip) {
			return fmt.Errorf("%w: direct IP %s is private or loopback", ErrBlockedHost, ip)
		}
		return nil
	}

	// Resolve host IPs
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("DNS resolution failed for %s: %w", host, err)
	}

	for _, ip := range ips {
		if IsPrivateOrLoopbackIP(ip) {
			return fmt.Errorf("%w: hostname %s resolves to private/loopback IP %s", ErrBlockedHost, host, ip)
		}
	}

	return nil
}

// SafeDialer creates a net.Dialer with timeout and optional SSRF validation.
func SafeDialer(blockPrivate bool) *net.Dialer {
	return &net.Dialer{
		Timeout:   15 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, c syscall.RawConn) error {
			if !blockPrivate {
				return nil
			}
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				host = address
			}
			ip := net.ParseIP(host)
			if ip != nil && IsPrivateOrLoopbackIP(ip) {
				return fmt.Errorf("%w: dial to private IP %s blocked", ErrBlockedHost, ip)
			}
			return nil
		},
	}
}
