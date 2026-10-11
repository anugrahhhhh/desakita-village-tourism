package app_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func seedDistrict(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `
		WITH p AS (INSERT INTO regions (kind, name) VALUES ('province', 'Prov') RETURNING id),
		     r AS (INSERT INTO regions (parent_id, kind, name) SELECT id, 'regency', 'Kab' FROM p RETURNING id)
		INSERT INTO regions (parent_id, kind, name) SELECT id, 'district', 'Kec' FROM r RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

var villageIDFromPath = regexp.MustCompile(`^/kelola/desa/(\d+)$`)

// createVillage submits the registration form and returns the new village id.
func (c *client) createVillage(name string, district int64) int64 {
	c.t.Helper()
	tok := c.csrfFrom("/kelola/desa/baru")
	r := c.post("/kelola/desa/baru", url.Values{
		"csrf_token": {tok}, "name": {name}, "district_id": {strconv.FormatInt(district, 10)},
		"contact_phone": {"0812-3456-7890"},
	})
	m := villageIDFromPath.FindStringSubmatch(r.location)
	if r.code != http.StatusSeeOther || m == nil {
		c.t.Fatalf("create village: %d → %q\n%s", r.code, r.location, r.body)
	}
	id, _ := strconv.ParseInt(m[1], 10, 64)
	return id
}

func (c *client) createPackage(villageID int64, name string) int64 {
	c.t.Helper()
	base := fmt.Sprintf("/kelola/desa/%d/paket", villageID)
	tok := c.csrfFrom(base + "/baru")
	r := c.post(base+"/baru", url.Values{
		"csrf_token": {tok}, "name": {name}, "price": {"150.000"},
		"duration_days": {"1"}, "duration_nights": {"0"}, "default_quota": {"10"},
	})
	m := regexp.MustCompile(`/paket/(\d+)/foto$`).FindStringSubmatch(r.location)
	if r.code != http.StatusSeeOther || m == nil {
		c.t.Fatalf("create package: %d → %q\n%s", r.code, r.location, r.body)
	}
	id, _ := strconv.ParseInt(m[1], 10, 64)
	return id
}

func (c *client) upload(path, csrf string, file []byte, consent bool) response {
	c.t.Helper()
	return c.uploadForm(path, csrf, file, consent, "")
}

func (c *client) uploadWithCaption(path, csrf string, file []byte, caption string) response {
	c.t.Helper()
	return c.uploadForm(path, csrf, file, true, caption)
}

func (c *client) uploadForm(path, csrf string, file []byte, consent bool, caption string) response {
	c.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("csrf_token", csrf) //nolint:errcheck
	if consent {
		mw.WriteField("consent", "1") //nolint:errcheck
	}
	if caption != "" {
		mw.WriteField("caption", caption) //nolint:errcheck
	}
	fw, _ := mw.CreateFormFile("photo", "foto.jpg")
	fw.Write(file) //nolint:errcheck
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, c.base+path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return c.do(req)
}

func pngImage(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 16, 12))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestManagerFlowAndPublicVisibility(t *testing.T) {
	srv, pool := newApp(t)
	district := seedDistrict(t, pool)
	owner := newClient(t, srv)
	owner.register("owner@example.com")

	vid := owner.createVillage("Desa Wisata Uji", district)
	if r := owner.get("/kelola"); !strings.Contains(r.body, "Desa Wisata Uji") || !strings.Contains(r.body, "Menunggu verifikasi") {
		t.Errorf("dashboard does not list pending village")
	}
	pid := owner.createPackage(vid, "Belajar Membatik")

	anon := newClient(t, srv)
	if r := anon.get("/desa/desa-wisata-uji"); r.code != http.StatusNotFound {
		t.Errorf("pending village public page: %d, want 404", r.code)
	}
	if r := owner.get("/desa/desa-wisata-uji"); r.code != http.StatusOK || !strings.Contains(r.body, "Pratinjau") {
		t.Errorf("owner preview: %d", r.code)
	}

	if _, err := pool.Exec(context.Background(), `UPDATE villages SET status = 'verified' WHERE id = $1`, vid); err != nil {
		t.Fatal(err)
	}
	r := anon.get("/desa/desa-wisata-uji")
	if r.code != http.StatusOK || !strings.Contains(r.body, "Belajar Membatik") || !strings.Contains(r.body, "Rp 150.000") {
		t.Errorf("verified village page: %d", r.code)
	}
	pkgPath := fmt.Sprintf("/desa/desa-wisata-uji/paket/%d", pid)
	if r := anon.get(pkgPath); r.code != http.StatusOK {
		t.Errorf("package page: %d", r.code)
	}

	tok := owner.csrfFrom(fmt.Sprintf("/kelola/desa/%d/paket", vid))
	owner.post(fmt.Sprintf("/kelola/desa/%d/paket/%d/status", vid, pid), url.Values{"csrf_token": {tok}, "active": {"0"}})
	if r := anon.get(pkgPath); r.code != http.StatusNotFound {
		t.Errorf("inactive package page: %d, want 404", r.code)
	}
	if r := anon.get("/desa/desa-wisata-uji"); strings.Contains(r.body, "Belajar Membatik") {
		t.Error("inactive package still listed on village page")
	}
}

func TestManagePagesRejectOtherUsers(t *testing.T) {
	srv, pool := newApp(t)
	district := seedDistrict(t, pool)
	owner := newClient(t, srv)
	owner.register("a@example.com")
	vidA := owner.createVillage("Desa A", district)
	pidA := owner.createPackage(vidA, "Paket A")

	intruder := newClient(t, srv)
	intruder.register("b@example.com")
	vidB := intruder.createVillage("Desa B", district)

	gets := []string{
		fmt.Sprintf("/kelola/desa/%d", vidA),
		fmt.Sprintf("/kelola/desa/%d/profil", vidA),
		fmt.Sprintf("/kelola/desa/%d/foto", vidA),
		fmt.Sprintf("/kelola/desa/%d/paket", vidA),
		fmt.Sprintf("/kelola/desa/%d/paket/%d", vidA, pidA),
		// Cross-village: B's manager addressing A's package under B's id.
		fmt.Sprintf("/kelola/desa/%d/paket/%d", vidB, pidA),
		fmt.Sprintf("/kelola/desa/%d/paket/%d/foto", vidB, pidA),
	}
	for _, p := range gets {
		if r := intruder.get(p); r.code != http.StatusNotFound {
			t.Errorf("GET %s by intruder: %d, want 404", p, r.code)
		}
	}

	tok := intruder.csrfFrom("/kelola")
	posts := map[string]url.Values{
		fmt.Sprintf("/kelola/desa/%d/profil", vidA):                {"name": {"Hacked"}, "district_id": {strconv.FormatInt(district, 10)}, "contact_phone": {"08123456789"}},
		fmt.Sprintf("/kelola/desa/%d/paket/%d", vidB, pidA):        {"name": {"Hacked"}, "price": {"1"}, "duration_days": {"1"}, "duration_nights": {"0"}, "default_quota": {"1"}},
		fmt.Sprintf("/kelola/desa/%d/paket/%d/status", vidB, pidA): {"active": {"0"}},
	}
	for p, vals := range posts {
		vals.Set("csrf_token", tok)
		if r := intruder.post(p, vals); r.code != http.StatusNotFound {
			t.Errorf("POST %s by intruder: %d, want 404", p, r.code)
		}
	}
	var name string
	if err := pool.QueryRow(context.Background(), `SELECT name FROM villages WHERE id = $1`, vidA).Scan(&name); err != nil || name != "Desa A" {
		t.Errorf("village A modified: %q %v", name, err)
	}
	var pkgName string
	var active bool
	if err := pool.QueryRow(context.Background(), `SELECT name, is_active FROM tour_packages WHERE id = $1`, pidA).Scan(&pkgName, &active); err != nil || pkgName != "Paket A" || !active {
		t.Errorf("package A modified: %q active=%v %v", pkgName, active, err)
	}

	if r := newClient(t, srv).get(fmt.Sprintf("/kelola/desa/%d", vidA)); r.code != http.StatusSeeOther {
		t.Errorf("anonymous manage page: %d, want redirect to login", r.code)
	}
}

func TestPhotoUpload(t *testing.T) {
	srv, pool := newApp(t)
	district := seedDistrict(t, pool)
	c := newClient(t, srv)
	c.register("foto@example.com")
	vid := c.createVillage("Desa Foto", district)
	page := fmt.Sprintf("/kelola/desa/%d/foto", vid)

	if r := c.upload(page, c.csrfFrom(page), pngImage(t), false); r.code != http.StatusUnprocessableEntity || !strings.Contains(r.body, "persetujuan") {
		t.Errorf("without consent: %d", r.code)
	}
	if r := c.upload(page, c.csrfFrom(page), []byte("<script>alert(1)</script>"), true); r.code != http.StatusUnprocessableEntity || !strings.Contains(r.body, "tidak didukung") {
		t.Errorf("script disguised as jpg: %d", r.code)
	}
	if r := c.upload(page, "forged", pngImage(t), true); r.code != http.StatusForbidden {
		t.Errorf("forged csrf on multipart: %d", r.code)
	}
	big := append(pngImage(t), make([]byte, 7<<20)...)
	if r := c.upload(page, c.csrfFrom(page), big, true); r.code != http.StatusRequestEntityTooLarge {
		t.Errorf("7 MB upload: %d, want 413", r.code)
	}

	if r := c.upload(page, c.csrfFrom(page), pngImage(t), true); r.code != http.StatusSeeOther {
		t.Fatalf("valid upload: %d\n%s", r.code, r.body)
	}
	m := regexp.MustCompile(`/media/([0-9a-f]{32}\.jpg)`).FindStringSubmatch(c.get(page).body)
	if m == nil {
		t.Fatal("uploaded photo not shown")
	}
	r := c.get("/media/" + m[1])
	if r.code != http.StatusOK || r.header.Get("Content-Type") != "image/jpeg" || r.header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("serve media: %d %v", r.code, r.header)
	}
}
