package service

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"

	"golang.org/x/image/webp"
)

// maxPhotoEdge rejects decoded images larger than this on either side so a
// small compressed file cannot expand into a huge bitmap.
const maxPhotoEdge = 2048

const jpegQuality = 85

// NormalizePhoto sniffs the real content type, decodes the image and
// re-encodes it. Re-encoding drops EXIF and other metadata. WebP is decoded
// and stored as JPEG, or as PNG when the image has transparency.
// The returned ext is jpg, png or (never) webp — webp is not written back.
func NormalizePhoto(data []byte, allowed map[string]struct{}, maxBytes int64) (body []byte, contentType, ext string, err error) {
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return nil, "", "", ErrPhotoTooLarge
	}
	if len(data) == 0 {
		return nil, "", "", ErrPhotoType
	}
	kind := sniffedImageType(data)
	if kind == "" {
		return nil, "", "", ErrPhotoType
	}
	if len(allowed) > 0 {
		if _, ok := allowed[kind]; !ok {
			return nil, "", "", ErrPhotoType
		}
	}
	img, err := decodeImage(kind, data)
	if err != nil {
		return nil, "", "", ErrPhotoType
	}
	b := img.Bounds()
	if b.Dx() < 1 || b.Dy() < 1 || b.Dx() > maxPhotoEdge || b.Dy() > maxPhotoEdge {
		return nil, "", "", validationError(map[string]string{"photo": "dimensions"})
	}
	var buf bytes.Buffer
	if kind == "image/png" || (kind == "image/webp" && imageHasAlpha(img)) {
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", "", fmt.Errorf("encode png: %w", err)
		}
		return buf.Bytes(), "image/png", "png", nil
	}
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, "", "", fmt.Errorf("encode jpeg: %w", err)
	}
	return buf.Bytes(), "image/jpeg", "jpg", nil
}

func sniffedImageType(data []byte) string {
	switch ct := http.DetectContentType(data); ct {
	case "image/jpeg", "image/png", "image/webp":
		return ct
	default:
		return ""
	}
}

func decodeImage(kind string, data []byte) (image.Image, error) {
	r := bytes.NewReader(data)
	switch kind {
	case "image/jpeg":
		return jpeg.Decode(r)
	case "image/png":
		return png.Decode(r)
	case "image/webp":
		return webp.Decode(r)
	default:
		return nil, ErrPhotoType
	}
}

func imageHasAlpha(img image.Image) bool {
	type opaque interface{ Opaque() bool }
	if o, ok := img.(opaque); ok {
		return !o.Opaque()
	}
	return false
}

func randomPhotoKey(ext string) (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("photo name: %w", err)
	}
	return hex.EncodeToString(buf[:]) + "." + ext, nil
}
