package httpx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
)

func jsonLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestAccessLog(t *testing.T) {
	var buf bytes.Buffer
	h := middleware.RequestID(AccessLog(jsonLogger(&buf))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		w.Write([]byte("hello")) //nolint:errcheck
	})))
	req := httptest.NewRequest(http.MethodPost, "/masuk?q=rahasia&phone=0812", strings.NewReader("password=s3cret&note=alergi"))
	h.ServeHTTP(httptest.NewRecorder(), req)

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log is not one JSON line: %v\n%s", err, buf.String())
	}
	for k, want := range map[string]any{"msg": "http request", "method": "POST", "path": "/masuk", "status": float64(418), "bytes": float64(5), "level": "INFO"} {
		if entry[k] != want {
			t.Errorf("%s = %v, want %v", k, entry[k], want)
		}
	}
	if entry["request_id"] == "" || entry["duration_ms"] == nil {
		t.Errorf("missing request_id/duration_ms: %v", entry)
	}
	for _, secret := range []string{"rahasia", "0812", "s3cret", "alergi"} {
		if strings.Contains(buf.String(), secret) {
			t.Errorf("log leaks %q: %s", secret, buf.String())
		}
	}
}

func TestAccessLogLevels(t *testing.T) {
	cases := map[string]struct {
		path   string
		status int
		level  string
	}{
		"probe":  {"/healthz", 200, "DEBUG"},
		"asset":  {"/static/app.css", 200, "DEBUG"},
		"server": {"/x", 500, "ERROR"},
	}
	for name, tc := range cases {
		var buf bytes.Buffer
		h := AccessLog(jsonLogger(&buf))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status) }))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tc.path, nil))
		if !strings.Contains(buf.String(), `"level":"`+tc.level+`"`) {
			t.Errorf("%s: %s", name, buf.String())
		}
	}
}

func TestRecover(t *testing.T) {
	var buf bytes.Buffer
	onPanic := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }
	h := middleware.RequestID(Recover(jsonLogger(&buf), onPanic)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d", rec.Code)
	}
	if !strings.Contains(buf.String(), `"panic":"boom"`) || !strings.Contains(buf.String(), `"stack"`) || !strings.Contains(buf.String(), `"request_id"`) {
		t.Errorf("panic not logged properly: %s", buf.String())
	}

	defer func() {
		if recover() != http.ErrAbortHandler {
			t.Error("ErrAbortHandler must propagate")
		}
	}()
	Recover(jsonLogger(&buf), onPanic)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
}
