package app_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestAdminDashboardAuditTrail(t *testing.T) {
	w := newBookingWorld(t)
	ctx := context.Background()
	adm := w.admin(t)

	var district int64
	if err := w.pool.QueryRow(ctx, "SELECT id FROM regions WHERE kind = 'district' LIMIT 1").Scan(&district); err != nil {
		t.Fatal(err)
	}
	pending := w.manager.createVillage("Desa Antre", district)

	// Overview: tiles and the verification queue.
	r := adm.get("/admin")
	for _, want := range []string{"Menunggu verifikasi", "Laporan terbuka", "Desa Antre", "Masuk hari ini", "Belum ada tindakan yang tercatat"} {
		if !strings.Contains(r.body, want) {
			t.Errorf("overview missing %q", want)
		}
	}

	// Verify the queued village, suspend the bookable one, close a report with a note.
	tok := adm.csrfFrom("/admin")
	adm.post(fmt.Sprintf("/admin/desa/%d/verifikasi", pending), url.Values{"csrf_token": {tok}})
	tok = adm.csrfFrom("/admin")
	adm.post(fmt.Sprintf("/admin/desa/%d/nonaktifkan", w.village), url.Values{"csrf_token": {tok}, "note": {"Laporan penipuan dari wisatawan"}})

	reporter := w.client(t)
	reporter.register("pelapor2@example.com")
	if _, err := w.pool.Exec(ctx, "INSERT INTO reports (reporter_id, target_type, target_id, reason) SELECT id, 'village', $1, 'Foto bukan milik desa' FROM users WHERE email = 'pelapor2@example.com'", pending); err != nil {
		t.Fatal(err)
	}
	var reportID int64
	w.pool.QueryRow(ctx, "SELECT id FROM reports ORDER BY id DESC LIMIT 1").Scan(&reportID) //nolint:errcheck
	tok = adm.csrfFrom("/admin/laporan")
	adm.post(fmt.Sprintf("/admin/laporan/%d/abaikan", reportID), url.Values{"csrf_token": {tok}, "note": {"Sudah dicek dengan pengelola"}})

	// Audit log page lists all three, newest first, with readable labels and the actor.
	r = adm.get("/admin/audit")
	if r.code != http.StatusOK {
		t.Fatalf("/admin/audit = %d", r.code)
	}
	list := auditList(r.body)
	iDismiss := strings.Index(list, "Laporan diabaikan")
	iSuspend := strings.Index(list, "Desa dinonaktifkan")
	iVerify := strings.Index(list, "Desa diverifikasi")
	if iDismiss < 0 || iSuspend < 0 || iVerify < 0 || !(iDismiss < iSuspend && iSuspend < iVerify) {
		t.Errorf("audit order/labels wrong: dismiss=%d suspend=%d verify=%d", iDismiss, iSuspend, iVerify)
	}
	for _, want := range []string{"Laporan penipuan dari wisatawan", "Sudah dicek dengan pengelola", "Terverifikasi", "WIB"} {
		if !strings.Contains(list, want) {
			t.Errorf("audit list missing %q", want)
		}
	}

	// Filters.
	if l := auditList(adm.get("/admin/audit?aksi=village.suspend").body); strings.Contains(l, "Desa diverifikasi") || !strings.Contains(l, "Laporan penipuan") {
		t.Error("action filter not applied")
	}
	if l := auditList(adm.get("/admin/audit?target=report").body); strings.Contains(l, "Desa dinonaktifkan") || !strings.Contains(l, "Laporan diabaikan") {
		t.Error("target filter not applied")
	}
	if r := adm.get("/admin/audit?aksi=drop-table&target=x&hal=-1"); r.code != http.StatusOK {
		t.Errorf("garbage filters: %d", r.code)
	}

	// Village detail shows its own history only.
	r = adm.get(fmt.Sprintf("/admin/desa/%d", w.village))
	if !strings.Contains(r.body, "Riwayat tindakan admin") || !strings.Contains(r.body, "Desa dinonaktifkan") || strings.Contains(r.body, "Laporan diabaikan") {
		t.Error("village history wrong")
	}

	// Report tabs: the dismissed report moved out of "open" with its note.
	if r := adm.get("/admin/laporan"); strings.Contains(r.body, "Foto bukan milik desa") {
		t.Error("dismissed report still listed as open")
	}
	if r := adm.get("/admin/laporan?status=dismissed"); !strings.Contains(r.body, "Foto bukan milik desa") || !strings.Contains(r.body, "Sudah dicek dengan pengelola") {
		t.Error("dismissed tab missing report or note")
	}
	if r := adm.get("/admin/laporan?status=bogus"); r.code != http.StatusNotFound {
		t.Errorf("bogus report tab: %d", r.code)
	}

	// Village list tabs.
	if r := adm.get("/admin/desa?status=suspended"); !strings.Contains(r.body, "Desa Pemesanan") {
		t.Error("suspended tab misses the suspended village")
	}
	if r := adm.get("/admin/desa?status=nope"); r.code != http.StatusNotFound {
		t.Errorf("bogus village tab: %d", r.code)
	}

	// The audit table really is append-only.
	if _, err := w.pool.Exec(ctx, "DELETE FROM audit_log"); err == nil {
		t.Error("audit_log rows could be deleted")
	}
	var n int
	w.pool.QueryRow(ctx, "SELECT count(*) FROM audit_log").Scan(&n) //nolint:errcheck
	if n != 3 {
		t.Errorf("audit rows = %d, want 3", n)
	}
}

func TestNewAdminPagesHiddenFromNonAdmins(t *testing.T) {
	w := newBookingWorld(t)
	for _, p := range []string{"/admin/audit", "/admin/desa", "/admin/desa?status=verified", "/admin/laporan?status=resolved"} {
		for name, c := range map[string]*client{"anonymous": w.client(t), "manager": w.manager} {
			if r := c.get(p); r.code != http.StatusNotFound {
				t.Errorf("%s GET %s = %d, want 404", name, p, r.code)
			}
		}
	}
}

// auditList returns the rendered audit entries, skipping the filter form whose options
// repeat every action label.
func auditList(body string) string {
	if i := strings.Index(body, `<ol class="divide-y`); i >= 0 {
		return body[i:]
	}
	return ""
}
