package security_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ashishsinghbora/magicloder/internal/security"
)

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"normal.txt", "normal.txt"},
		{"../../etc/passwd", "passwd"},
		{"..\\..\\windows\\system32\\calc.exe", "calc.exe"},
		{"foo/bar/baz.zip", "baz.zip"},
		{"con.txt", "_con.txt"},
		{"AUX", "_AUX"},
		{"NUL.tar.gz", "_NUL.tar.gz"},
		{"com1", "_com1"},
		{"test:file*with?bad<chars>|.pdf", "test_file_with_bad_chars__.pdf"},
		{"file with spaces.mp4", "file with spaces.mp4"},
		{"   padded.iso   ", "padded.iso"},
		{"..hidden", "hidden"},
		{"", "download"},
		{"   ", "download"},
		{"utf8_日本語_файл_🚀.bin", "utf8_日本語_файл_🚀.bin"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := security.SanitizeFilename(tc.input)
			if got != tc.expected {
				t.Fatalf("SanitizeFilename(%q) = %q, expected %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestFilenameFromURL(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://example.com/files/archive.zip", "archive.zip"},
		{"https://example.com/files/archive.zip?token=123", "archive.zip"},
		{"https://example.com/", "download"},
		{"https://example.com/test%20file.pdf", "test file.pdf"},
		{"https://example.com/../../secret.txt", "secret.txt"},
		{"invalid://url::123", "download"},
	}

	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			got := security.FilenameFromURL(tc.url)
			if got != tc.expected {
				t.Fatalf("FilenameFromURL(%q) = %q, expected %q", tc.url, got, tc.expected)
			}
		})
	}
}

func TestFilenameFromContentDisposition(t *testing.T) {
	tests := []struct {
		header   string
		expected string
	}{
		{`attachment; filename="report.pdf"`, "report.pdf"},
		{`attachment; filename="../evil.sh"`, "evil.sh"},
		{`attachment; filename*=UTF-8''my%20document.docx`, "my document.docx"},
		{`inline`, ""},
		{`malformed; header; without; filename`, ""},
	}

	for _, tc := range tests {
		t.Run(tc.header, func(t *testing.T) {
			got := security.FilenameFromContentDisposition(tc.header)
			if got != tc.expected {
				t.Fatalf("FilenameFromContentDisposition(%q) = %q, expected %q", tc.header, got, tc.expected)
			}
		})
	}
}

func TestResolveSafeDestination(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "magicloder-security-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cleanTempDir, _ := filepath.Abs(tempDir)

	// Valid case
	res, err := security.ResolveSafeDestination(tempDir, "sample.iso")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(cleanTempDir, "sample.iso")
	if res != expected {
		t.Fatalf("expected %s, got %s", expected, res)
	}

	// Path traversal attempt
	res, err = security.ResolveSafeDestination(tempDir, "../../../etc/shadow")
	if err != nil {
		t.Fatalf("unexpected error on traversal attempt: %v", err)
	}
	if !strings.HasPrefix(res, cleanTempDir) {
		t.Fatalf("result escaped temp dir: %s", res)
	}
	if filepath.Base(res) != "shadow" {
		t.Fatalf("expected base name shadow, got %s", filepath.Base(res))
	}
}

func TestAllocateNonCollidingPath(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "magicloder-collision-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	baseFile := filepath.Join(tempDir, "output.bin")

	// Initially does not exist
	p1, err := security.AllocateNonCollidingPath(baseFile)
	if err != nil || p1 != baseFile {
		t.Fatalf("expected original path when not existing, got %s (%v)", p1, err)
	}

	// Create base file
	if err := os.WriteFile(baseFile, []byte("data"), 0644); err != nil {
		t.Fatalf("failed to write base file: %v", err)
	}

	p2, err := security.AllocateNonCollidingPath(baseFile)
	expectedP2 := filepath.Join(tempDir, "output (1).bin")
	if err != nil || p2 != expectedP2 {
		t.Fatalf("expected %s, got %s (%v)", expectedP2, p2, err)
	}

	// Create first collision
	if err := os.WriteFile(p2, []byte("data2"), 0644); err != nil {
		t.Fatalf("failed to write collision file: %v", err)
	}

	p3, err := security.AllocateNonCollidingPath(baseFile)
	expectedP3 := filepath.Join(tempDir, "output (2).bin")
	if err != nil || p3 != expectedP3 {
		t.Fatalf("expected %s, got %s (%v)", expectedP3, p3, err)
	}
}
