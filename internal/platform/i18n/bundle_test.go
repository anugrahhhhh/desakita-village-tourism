package i18n

import (
	"context"
	"testing"
)

func TestNegotiate(t *testing.T) {
	cases := map[string]string{
		"":                                 "",
		"en-US,en;q=0.9":                   "en",
		"id-ID,id;q=0.9,en-US;q=0.8":       "id",
		"fr-FR,fr;q=0.9,en;q=0.5,id;q=0.4": "id", // Indonesian-first whenever accepted
		"en-US,en;q=0.9,id;q=0.8":          "id",
		"fr-FR,fr;q=0.9,en;q=0.5":          "en",
		"de,ja":                            "",
		"en;q=0,id;q=0.1":                  "id",
		"in":                               "id",
		"en;q=abc,id":                      "id",
		"EN-gb":                            "en",
		"en;q=0.5, id;q=0.5":               "id",
	}
	for header, want := range cases {
		if got := Negotiate(header); got != want {
			t.Errorf("Negotiate(%q) = %q, want %q", header, got, want)
		}
	}
}

func TestLangFrom(t *testing.T) {
	ctx := context.Background()
	if LangFrom(ctx) != Default {
		t.Error("empty context should use the default language")
	}
	if LangFrom(WithLang(ctx, "en")) != "en" || LangFrom(WithLang(ctx, "xx")) != Default {
		t.Error("unexpected language from context")
	}
}

func TestPluralOne(t *testing.T) {
	tr := &Translator{lang: "en", messages: map[string]string{"r": "%d reviews", "r.one": "%d review", "x": "%d of %d"}}
	if got := tr.T("r", 1); got != "1 review" {
		t.Errorf("one: %q", got)
	}
	if got := tr.T("r", int32(1)); got != "1 review" {
		t.Errorf("int32 one: %q", got)
	}
	if got := tr.T("r", 0); got != "0 reviews" {
		t.Errorf("zero: %q", got)
	}
	if got := tr.T("x", 1, 3); got != "1 of 3" {
		t.Errorf("no .one key: %q", got)
	}
}
