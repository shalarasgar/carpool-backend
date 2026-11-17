// Package media stores user-uploaded images behind a small interface, so local disk can
// later be swapped for S3-compatible storage without touching the services.
package media

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrTooLarge means the upload exceeds MaxBytes.
	ErrTooLarge = errors.New("file too large")
	// ErrUnsupported means the content is not a JPEG, PNG, or WebP image.
	ErrUnsupported = errors.New("unsupported media")
)

// MaxBytes is the upload limit for one image.
const MaxBytes = 5 << 20

// URLPrefix is the public path under which stored files are served.
const URLPrefix = "/media/"

var extensions = map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp"}

// Storage saves and removes public images.
type Storage interface {
	Save(ctx context.Context, kind string, data []byte) (url string, err error)
	Delete(ctx context.Context, url string) error
}

// Detect returns the extension for a supported image, judged by content, not by name.
func Detect(data []byte) (string, error) {
	if len(data) > MaxBytes {
		return "", ErrTooLarge
	}
	ext, ok := extensions[http.DetectContentType(data)]
	if !ok {
		return "", ErrUnsupported
	}
	return ext, nil
}

// Disk stores files in a local directory.
type Disk struct {
	Dir string
}

// Save writes data under a random name and returns its public URL path.
func (d Disk) Save(_ context.Context, kind string, data []byte) (string, error) {
	ext, err := Detect(data)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(d.Dir, 0o755); err != nil {
		return "", fmt.Errorf("media dir: %w", err)
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("media name: %w", err)
	}
	name := kind + "_" + hex.EncodeToString(buf) + ext
	if err := os.WriteFile(filepath.Join(d.Dir, name), data, 0o644); err != nil {
		return "", fmt.Errorf("media write: %w", err)
	}
	return URLPrefix + name, nil
}

// Delete removes a file previously returned by Save. Unknown URLs are ignored.
func (d Disk) Delete(_ context.Context, url string) error {
	name := strings.TrimPrefix(url, URLPrefix)
	if url == "" || name == url || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return nil
	}
	err := os.Remove(filepath.Join(d.Dir, name))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("media delete: %w", err)
	}
	return nil
}
