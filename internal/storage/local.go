// Package storage saves user uploads. The local disk implementation is the
// default; an S3 implementation can satisfy the same Store interface later.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrInvalidKey is returned when a key is not a generated photo file name.
var ErrInvalidKey = errors.New("invalid storage key")

// safePhotoKey is the only name shape Put, Open and Delete accept.
var safePhotoKey = regexp.MustCompile(`^[a-f0-9]{32}\.(jpg|png|webp)$`)

// Store is the upload backend. Keys are opaque file names, never user paths.
type Store interface {
	Put(ctx context.Context, key, contentType string, body []byte) error
	Delete(ctx context.Context, key string) error
	Open(key string) (io.ReadCloser, error)
	PublicPath(key string) string
	Key(publicPath string) (string, error)
}

// Local stores objects as files in one directory.
type Local struct {
	dir    string
	prefix string
}

// NewLocal creates dir if needed. urlPrefix is the public path prefix, such
// as /v1/media/photos, with no trailing slash.
func NewLocal(dir, urlPrefix string) (*Local, error) {
	if strings.TrimSpace(dir) == "" || strings.TrimSpace(urlPrefix) == "" {
		return nil, fmt.Errorf("photo storage dir and url prefix are required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create photo dir: %w", err)
	}
	return &Local{dir: dir, prefix: strings.TrimRight(urlPrefix, "/")}, nil
}

// Put writes body under key. contentType is accepted so an object-store
// implementation can persist it; the local server derives the type from the
// key extension when serving the file.
func (l *Local) Put(ctx context.Context, key, contentType string, body []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if contentType == "" || !safePhotoKey.MatchString(key) {
		return ErrInvalidKey
	}
	tmp, err := os.CreateTemp(l.dir, "upload-*")
	if err != nil {
		return fmt.Errorf("create photo temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write photo: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close photo: %w", err)
	}
	dest := filepath.Join(l.dir, key)
	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("rename photo: %w", err)
	}
	cleanup = false
	return nil
}

// Delete removes key. A missing file is success.
func (l *Local) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !safePhotoKey.MatchString(key) {
		return ErrInvalidKey
	}
	err := os.Remove(filepath.Join(l.dir, key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Open returns the file for a safe key. The caller closes it.
func (l *Local) Open(key string) (io.ReadCloser, error) {
	if !safePhotoKey.MatchString(key) || filepath.Base(key) != key {
		return nil, ErrInvalidKey
	}
	f, err := os.Open(filepath.Join(l.dir, key))
	if err != nil {
		return nil, err
	}
	return f, nil
}

// PublicPath is the URL path stored on the user and returned to clients.
func (l *Local) PublicPath(key string) string {
	return l.prefix + "/" + key
}

// Key extracts the file name from a path previously produced by PublicPath.
func (l *Local) Key(publicPath string) (string, error) {
	rest, ok := strings.CutPrefix(publicPath, l.prefix+"/")
	if !ok || !safePhotoKey.MatchString(rest) {
		return "", ErrInvalidKey
	}
	return rest, nil
}
