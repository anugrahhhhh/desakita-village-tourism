// Package upload validates, sanitizes and stores user photos.
//
// Every accepted image is decoded, auto-rotated from EXIF orientation, downscaled and
// re-encoded as JPEG. Re-encoding drops all metadata, including GPS coordinates of the
// people photographed.
package upload

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // register decoders for DecodeConfig
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/disintegration/imaging"
	_ "golang.org/x/image/webp"
)

const (
	MaxBytes     = 5 << 20
	MaxDimension = 6000
	// MaxEdge is the longest side after downscaling; plenty for phone screens.
	MaxEdge     = 1600
	jpegQuality = 82
	OutputMIME  = "image/jpeg"
)

var (
	ErrTooLarge      = errors.New("upload: file too large")
	ErrUnsupported   = errors.New("upload: unsupported file type")
	ErrTooManyPixels = errors.New("upload: image dimensions too large")
	ErrCorrupt       = errors.New("upload: image could not be decoded")
)

var allowedMIME = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true}

// Processed is a sanitized image ready to store.
type Processed struct {
	Data          []byte
	Width, Height int
}

// Process validates r by content (never by extension) and returns a re-encoded JPEG.
func Process(r io.Reader) (Processed, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return Processed{}, ErrTooLarge
		}
		return Processed{}, err
	}
	if len(raw) > MaxBytes {
		return Processed{}, ErrTooLarge
	}
	if !allowedMIME[http.DetectContentType(raw)] {
		return Processed{}, ErrUnsupported
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return Processed{}, ErrCorrupt
	}
	// Check before decoding so a tiny file can't expand into gigabytes of pixels.
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > MaxDimension || cfg.Height > MaxDimension {
		return Processed{}, ErrTooManyPixels
	}
	img, err := imaging.Decode(bytes.NewReader(raw), imaging.AutoOrientation(true))
	if err != nil {
		return Processed{}, ErrCorrupt
	}
	b := img.Bounds()
	if b.Dx() > MaxEdge || b.Dy() > MaxEdge {
		img = imaging.Fit(img, MaxEdge, MaxEdge, imaging.Lanczos)
	}
	var out bytes.Buffer
	if err := imaging.Encode(&out, img, imaging.JPEG, imaging.JPEGQuality(jpegQuality)); err != nil {
		return Processed{}, fmt.Errorf("upload: encode: %w", err)
	}
	ob := img.Bounds()
	return Processed{Data: out.Bytes(), Width: ob.Dx(), Height: ob.Dy()}, nil
}

// Store keeps processed files. LocalStore is the MVP; an S3-compatible store can replace it.
type Store interface {
	Save(name string, data []byte) error
	Delete(name string) error
	// Path returns the local file path for serving, or "" if name is invalid.
	Path(name string) string
}

var namePattern = regexp.MustCompile(`^[0-9a-f]{32}\.jpg$`)

// NewName returns a random, unguessable file name.
func NewName() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b) + ".jpg"
}

// ValidName reports whether name could have come from NewName (guards path traversal).
func ValidName(name string) bool { return namePattern.MatchString(name) }

type LocalStore struct{ dir string }

func NewLocalStore(dir string) (*LocalStore, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("upload dir: %w", err)
	}
	return &LocalStore{dir: dir}, nil
}

func (s *LocalStore) Save(name string, data []byte) error {
	if !ValidName(name) {
		return fmt.Errorf("upload: invalid name %q", name)
	}
	// Write to a temp file then rename, so readers never see a partial image.
	tmp, err := os.CreateTemp(s.dir, "tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(s.dir, name))
}

func (s *LocalStore) Delete(name string) error {
	if !ValidName(name) {
		return fmt.Errorf("upload: invalid name %q", name)
	}
	err := os.Remove(filepath.Join(s.dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *LocalStore) Path(name string) string {
	if !ValidName(name) {
		return ""
	}
	return filepath.Join(s.dir, name)
}

// ServeHandler serves stored files at /media/{name} with safe headers.
func ServeHandler(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(r.URL.Path)
		p := store.Path(name)
		if p == "" {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(p)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", OutputMIME)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Names are random and files immutable.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		http.ServeContent(w, r, "", st.ModTime(), f)
	}
}

// SaveImage processes r and stores the result under a fresh name.
func SaveImage(store Store, r io.Reader) (name string, size int, err error) {
	p, err := Process(r)
	if err != nil {
		return "", 0, err
	}
	name = NewName()
	if err := store.Save(name, p.Data); err != nil {
		return "", 0, fmt.Errorf("upload: save: %w", err)
	}
	return name, len(p.Data), nil
}

// MessageKey maps an upload error to an i18n key, or "" if err is not a user error.
func MessageKey(err error) string {
	switch {
	case errors.Is(err, ErrTooLarge):
		return "upload.too_large"
	case errors.Is(err, ErrUnsupported):
		return "upload.unsupported"
	case errors.Is(err, ErrTooManyPixels):
		return "upload.too_many_pixels"
	case errors.Is(err, ErrCorrupt):
		return "upload.corrupt"
	}
	return ""
}
