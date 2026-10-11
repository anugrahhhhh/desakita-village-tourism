package upload

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// jpegWithExif returns a w×h JPEG carrying an EXIF APP1 segment with the given orientation
// and a recognisable marker string standing in for GPS data.
func jpegWithExif(t *testing.T, w, h int, orientation uint16) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var plain bytes.Buffer
	if err := jpeg.Encode(&plain, img, nil); err != nil {
		t.Fatal(err)
	}

	// Minimal little-endian TIFF: one IFD entry (Orientation, SHORT, 1 value).
	var tiff bytes.Buffer
	tiff.WriteString("II*\x00")
	binary.Write(&tiff, binary.LittleEndian, uint32(8))      //nolint:errcheck
	binary.Write(&tiff, binary.LittleEndian, uint16(1))      //nolint:errcheck
	binary.Write(&tiff, binary.LittleEndian, uint16(0x0112)) //nolint:errcheck
	binary.Write(&tiff, binary.LittleEndian, uint16(3))      //nolint:errcheck
	binary.Write(&tiff, binary.LittleEndian, uint32(1))      //nolint:errcheck
	binary.Write(&tiff, binary.LittleEndian, orientation)    //nolint:errcheck
	binary.Write(&tiff, binary.LittleEndian, uint16(0))      //nolint:errcheck
	binary.Write(&tiff, binary.LittleEndian, uint32(0))      //nolint:errcheck
	tiff.WriteString("GPS-SECRET-MARKER")

	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	var out bytes.Buffer
	out.Write([]byte{0xFF, 0xD8, 0xFF, 0xE1})
	binary.Write(&out, binary.BigEndian, uint16(len(payload)+2)) //nolint:errcheck
	out.Write(payload)
	out.Write(plain.Bytes()[2:]) // skip the original SOI
	return out.Bytes()
}

func TestProcessAcceptsImagesAndReencodesJPEG(t *testing.T) {
	p, err := Process(bytes.NewReader(pngBytes(t, 40, 30)))
	if err != nil {
		t.Fatal(err)
	}
	if http.DetectContentType(p.Data) != "image/jpeg" || p.Width != 40 || p.Height != 30 {
		t.Errorf("got %s %dx%d", http.DetectContentType(p.Data), p.Width, p.Height)
	}
}

func TestProcessStripsExifAndAppliesOrientation(t *testing.T) {
	src := jpegWithExif(t, 20, 10, 6) // 6 = rotate 90° clockwise
	if !bytes.Contains(src, []byte("GPS-SECRET-MARKER")) {
		t.Fatal("fixture lacks marker")
	}
	p, err := Process(bytes.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(p.Data, []byte("GPS-SECRET-MARKER")) || bytes.Contains(p.Data, []byte("Exif\x00\x00")) {
		t.Error("output still contains EXIF metadata")
	}
	if p.Width != 10 || p.Height != 20 {
		t.Errorf("orientation not applied: %dx%d, want 10x20", p.Width, p.Height)
	}
}

func TestProcessDownscalesLargeImages(t *testing.T) {
	p, err := Process(bytes.NewReader(pngBytes(t, 3200, 800)))
	if err != nil {
		t.Fatal(err)
	}
	if p.Width != MaxEdge || p.Height != 400 {
		t.Errorf("got %dx%d, want %dx400", p.Width, p.Height, MaxEdge)
	}
}

func TestProcessRejects(t *testing.T) {
	cases := map[string]struct {
		data []byte
		want error
	}{
		"text renamed to jpg": {[]byte("<html><script>alert(1)</script></html>"), ErrUnsupported},
		"svg":                 {[]byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), ErrUnsupported},
		"gif":                 {[]byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"), ErrUnsupported},
		"truncated png":       {pngBytes(t, 10, 10)[:30], ErrCorrupt},
		"too wide":            {pngBytes(t, MaxDimension+1, 1), ErrTooManyPixels},
		"too large":           {append(pngBytes(t, 1, 1), make([]byte, MaxBytes)...), ErrTooLarge},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Process(bytes.NewReader(tc.data)); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestLocalStoreAndServe(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	name := NewName()
	if err := store.Save(name, []byte("jpegdata")); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../etc/passwd", "x.jpg", name + "/..", "ABCDEF0123456789ABCDEF0123456789.jpg"} {
		if store.Path(bad) != "" {
			t.Errorf("Path(%q) accepted", bad)
		}
		if err := store.Save(bad, nil); err == nil {
			t.Errorf("Save(%q) accepted", bad)
		}
	}

	h := ServeHandler(store)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/media/"+name, nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "jpegdata" {
		t.Fatalf("serve: %d %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("headers: %v", rec.Header())
	}
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/media/..%2F..%2Fgo.mod", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("traversal: %d", rec.Code)
	}

	if err := store.Delete(name); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(store.Path(name)), name)); !os.IsNotExist(err) {
		t.Error("file not deleted")
	}
	if err := store.Delete(name); err != nil {
		t.Errorf("deleting a missing file should be a no-op: %v", err)
	}
}
