package security

import (
	"errors"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var (
	ErrPathEscape       = errors.New("destination path escapes target directory")
	ErrEmptyFilename    = errors.New("resolved filename is empty")
	ErrReservedName     = errors.New("filename uses reserved OS device name")
	ErrInvalidDirectory = errors.New("target directory is invalid")
)

var (
	// Windows reserved file names
	windowsReservedNames = map[string]bool{
		"CON": true, "PRN": true, "AUX": true, "NUL": true,
		"COM1": true, "COM2": true, "COM3": true, "COM4": true,
		"COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
		"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
		"LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
	}

	// Characters illegal in filenames on Windows and Unix-like filesystems
	illegalCharsRegex = regexp.MustCompile(`[\x00-\x1f\x7f/\\:*\?"<>|]`)
)

// SanitizeFilename cleans an untrusted filename string, preventing path traversal,
// reserved device names, and invalid characters while preserving valid Unicode.
func SanitizeFilename(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "download"
	}

	// Remove path separators and relative segments
	raw = filepath.Base(filepath.Clean(strings.ReplaceAll(raw, "\\", "/")))
	for strings.HasPrefix(raw, ".") {
		raw = strings.TrimPrefix(raw, ".")
	}

	// Replace illegal characters with underscores
	cleaned := illegalCharsRegex.ReplaceAllString(raw, "_")

	// Filter out non-printable runes
	var b strings.Builder
	for _, r := range cleaned {
		if unicode.IsPrint(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	result := strings.TrimSpace(b.String())

	// Strip trailing dots and spaces (problematic on Windows)
	result = strings.TrimRight(result, ". ")
	if result == "" {
		return "download"
	}

	// Check against Windows reserved base names (e.g. AUX, NUL, NUL.tar.gz, COM1.txt)
	stem := strings.ToUpper(strings.Split(result, ".")[0])
	if windowsReservedNames[stem] {
		result = "_" + result
	}

	// Truncate length if excessively long (e.g., max 255 bytes)
	if len(result) > 240 {
		ext := filepath.Ext(result)
		maxBase := 240 - len(ext)
		if maxBase > 0 && len(result) > maxBase {
			result = result[:maxBase] + ext
		} else {
			result = result[:240]
		}
	}

	return result
}

// FilenameFromURL extracts and sanitizes a filename from a URL string.
func FilenameFromURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "download"
	}

	path := parsed.Path
	if unescaped, err := url.PathUnescape(path); err == nil {
		path = unescaped
	}

	cleaned := filepath.Base(filepath.Clean(path))
	if cleaned == "" || cleaned == "/" || cleaned == "." {
		return "download"
	}

	return SanitizeFilename(cleaned)
}

// FilenameFromContentDisposition extracts the filename from an HTTP Content-Disposition header.
func FilenameFromContentDisposition(header string) string {
	if header == "" {
		return ""
	}

	_, params, err := mime.ParseMediaType(header)
	if err != nil {
		return ""
	}

	// Prefer filename* parameter (RFC 5987 / RFC 6266) if present
	if fnStar, ok := params["filename*"]; ok && fnStar != "" {
		return SanitizeFilename(fnStar)
	}

	if fn, ok := params["filename"]; ok && fn != "" {
		return SanitizeFilename(fn)
	}

	return ""
}

// ResolveSafeDestination computes an absolute, safe destination filepath within baseDir.
// It guarantees that the resulting path is strictly within baseDir and prevents directory traversal.
func ResolveSafeDestination(baseDir, filename string) (string, error) {
	if strings.TrimSpace(baseDir) == "" {
		return "", fmt.Errorf("%w: baseDir cannot be empty", ErrInvalidDirectory)
	}

	cleanBase, err := filepath.Abs(filepath.Clean(baseDir))
	if err != nil {
		return "", fmt.Errorf("%w: failed to resolve base dir: %v", ErrInvalidDirectory, err)
	}

	safeName := SanitizeFilename(filename)
	if safeName == "" {
		return "", ErrEmptyFilename
	}

	finalPath := filepath.Clean(filepath.Join(cleanBase, safeName))

	// Ensure finalPath is strictly inside cleanBase
	rel, err := filepath.Rel(cleanBase, finalPath)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "." && cleanBase != finalPath {
		return "", fmt.Errorf("%w: path %s escapes %s", ErrPathEscape, finalPath, cleanBase)
	}

	return finalPath, nil
}

// AllocateNonCollidingPath returns an available file path in baseDir. If candidate exists,
// it appends a sequential suffix such as "(1)", "(2)" until an unused path is found.
func AllocateNonCollidingPath(targetPath string) (string, error) {
	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		return targetPath, nil
	}

	dir := filepath.Dir(targetPath)
	ext := filepath.Ext(targetPath)
	base := strings.TrimSuffix(filepath.Base(targetPath), ext)

	for i := 1; i <= 10000; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("unable to allocate unique filename for %s after 10000 attempts", targetPath)
}
