package csrf

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/alexedwards/scs/v2/memstore"
)

// newServer returns a server where GET /token prints the token and POST /submit is protected.
func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	sm := scs.New()
	sm.Store = memstore.New()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /token", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, Token(r.Context(), sm)) //nolint:errcheck
	})
	mux.HandleFunc("POST /submit", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }) //nolint:errcheck
	mux.HandleFunc("POST /rotate", func(w http.ResponseWriter, r *http.Request) { Rotate(r.Context(), sm) })
	fail := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) })
	srv := httptest.NewServer(sm.LoadAndSave(Protect(sm, fail)(mux)))
	t.Cleanup(srv.Close)
	return srv
}

func newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

func getToken(t *testing.T, c *http.Client, base string) string {
	t.Helper()
	resp, err := c.Get(base + "/token")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func postForm(t *testing.T, c *http.Client, u string, vals url.Values, header map[string]string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, u, strings.NewReader(vals.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestProtect(t *testing.T) {
	srv := newServer(t)

	t.Run("missing token is rejected", func(t *testing.T) {
		c := newClient(t)
		getToken(t, c, srv.URL)
		if code := postForm(t, c, srv.URL+"/submit", nil, nil); code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", code)
		}
	})

	t.Run("no session at all is rejected", func(t *testing.T) {
		c := newClient(t)
		if code := postForm(t, c, srv.URL+"/submit", url.Values{FieldName: {""}}, nil); code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", code)
		}
	})

	t.Run("form field token is accepted", func(t *testing.T) {
		c := newClient(t)
		tok := getToken(t, c, srv.URL)
		if code := postForm(t, c, srv.URL+"/submit", url.Values{FieldName: {tok}}, nil); code != http.StatusOK {
			t.Errorf("status = %d, want 200", code)
		}
	})

	t.Run("header token is accepted", func(t *testing.T) {
		c := newClient(t)
		tok := getToken(t, c, srv.URL)
		if code := postForm(t, c, srv.URL+"/submit", nil, map[string]string{HeaderName: tok}); code != http.StatusOK {
			t.Errorf("status = %d, want 200", code)
		}
	})

	t.Run("another session's token is rejected", func(t *testing.T) {
		attacker, victim := newClient(t), newClient(t)
		tok := getToken(t, attacker, srv.URL)
		getToken(t, victim, srv.URL)
		if code := postForm(t, victim, srv.URL+"/submit", url.Values{FieldName: {tok}}, nil); code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", code)
		}
	})

	t.Run("rotated token is no longer valid", func(t *testing.T) {
		c := newClient(t)
		old := getToken(t, c, srv.URL)
		postForm(t, c, srv.URL+"/rotate", url.Values{FieldName: {old}}, nil)
		if code := postForm(t, c, srv.URL+"/submit", url.Values{FieldName: {old}}, nil); code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", code)
		}
		if fresh := getToken(t, c, srv.URL); fresh == old || fresh == "" {
			t.Errorf("token not rotated")
		}
	})
}
