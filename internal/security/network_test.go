package security_test

import (
	"net"
	"testing"

	"github.com/ashishsinghbora/magicloder/internal/security"
)

func TestValidateURLScheme(t *testing.T) {
	tests := []struct {
		url       string
		expectErr bool
	}{
		{"http://example.com/file.zip", false},
		{"https://example.com/file.zip", false},
		{"ftp://example.com/file.zip", true},
		{"file:///etc/passwd", true},
		{"gopher://example.com", true},
		{"http://", true},
		{"", true},
		{"://bad-url", true},
	}

	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			_, err := security.ValidateURLScheme(tc.url)
			if tc.expectErr && err == nil {
				t.Fatalf("expected error for %q, got nil", tc.url)
			}
			if !tc.expectErr && err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.url, err)
			}
		})
	}
}

func TestIsPrivateOrLoopbackIP(t *testing.T) {
	tests := []struct {
		ip       string
		expected bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"10.0.0.1", true},
		{"192.168.1.100", true},
		{"172.16.0.5", true},
		{"169.254.1.1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"93.184.216.34", false},
	}

	for _, tc := range tests {
		t.Run(tc.ip, func(t *testing.T) {
			parsed := net.ParseIP(tc.ip)
			if parsed == nil {
				t.Fatalf("failed to parse IP %s", tc.ip)
			}
			got := security.IsPrivateOrLoopbackIP(parsed)
			if got != tc.expected {
				t.Fatalf("IsPrivateOrLoopbackIP(%s) = %v, expected %v", tc.ip, got, tc.expected)
			}
		})
	}
}
