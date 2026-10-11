package phone

import (
	"net/url"
	"strings"
	"testing"
)

func TestNormalizeWA(t *testing.T) {
	cases := map[string]string{
		"081234567890":      "6281234567890",
		"0812-3456-7890":    "6281234567890",
		"0812 3456 7890":    "6281234567890",
		"+62 812 3456 7890": "6281234567890",
		"62812345678":       "62812345678",
		"(+62) 812-345-678": "62812345678",
		"81234567890":       "6281234567890",
		"0274 123456":       "62274123456", // landline
	}
	for in, want := range cases {
		got, ok := NormalizeWA(in)
		if !ok || got != want {
			t.Errorf("NormalizeWA(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "abc", "0812", "+1 415 555 0100", "1234567890", "0812345678901234567"} {
		if got, ok := NormalizeWA(bad); ok {
			t.Errorf("NormalizeWA(%q) = %q, want rejection", bad, got)
		}
	}
}

func TestWALink(t *testing.T) {
	msg := "Halo Sari, pemesanan #12 & paket \"Batik\" dikonfirmasi: 15/3 + 2 orang?"
	link := WALink("0812-3456-7890", msg)
	if !strings.HasPrefix(link, "https://wa.me/6281234567890?text=") {
		t.Fatalf("link = %q", link)
	}
	raw := strings.TrimPrefix(link, "https://wa.me/6281234567890?text=")
	if strings.ContainsAny(raw, " &\"+#?/") {
		t.Errorf("text not fully encoded: %q", raw)
	}
	decoded, err := url.QueryUnescape(raw)
	if err != nil || decoded != msg {
		t.Errorf("round trip = %q, %v", decoded, err)
	}
	if WALink("abc", msg) != "" {
		t.Error("invalid number should give empty link")
	}
}
