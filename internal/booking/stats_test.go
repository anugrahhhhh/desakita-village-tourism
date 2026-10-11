package booking_test

import (
	"errors"
	"testing"
	"time"

	"desakita/internal/booking"
)

func TestVillageStats(t *testing.T) {
	e := newEnv(t)
	f := e.fixture(50) // price 100000 per person
	traveler := e.user()
	at := func(day, hour int) { e.clock.Set(time.Date(2030, 3, day, hour, 0, 0, 0, jakarta)) }

	// All requests arrive on 03-10 at 10:00 (startTime).
	completed := e.book(traveler, f.pkg, day(4), 3)
	rejected := e.book(traveler, f.pkg, day(5), 2)
	expired := e.book(e.user(), f.pkg, day(2), 1)
	cancelled := e.book(e.user(), f.pkg, day(6), 2)
	e.book(e.user(), f.pkg, day(20), 1) // stays pending

	at(10, 11)
	e.svc.Confirm(e.ctx, f.manager, completed.ID) //nolint:errcheck // answered after 1 h
	at(10, 12)
	e.svc.Confirm(e.ctx, f.manager, cancelled.ID) //nolint:errcheck // 2 h
	at(10, 13)
	e.svc.Reject(e.ctx, f.manager, rejected.ID, "") //nolint:errcheck // 3 h
	at(11, 9)
	e.svc.CancelByManager(e.ctx, f.manager, cancelled.ID, "") //nolint:errcheck
	at(12, 0)
	if n, _ := e.svc.ExpireDue(e.ctx); n != 1 {
		t.Fatalf("expired %d", n)
	}
	at(14, 9)
	if _, err := e.svc.Complete(e.ctx, f.manager, completed.ID); err != nil {
		t.Fatal(err)
	}
	_ = expired

	// Older completed visits: one inside the 365-day period and the chart, one before both.
	old := func(created, start string, party int) {
		e.exec(`INSERT INTO bookings (package_id, village_id, start_date, party_size, total_price_idr, contact_name, contact_phone,
			status, expires_at, decided_at, completed_at, created_at)
			VALUES ($1, $2, $3::date, $4, $4 * 100000, 'Lama', '0812', 'completed', $5::timestamptz, $5::timestamptz, $3::date + 1, $5::timestamptz)`,
			f.pkg, f.village, start, party, created)
	}
	old("2029-06-01 10:00+07", "2029-06-10", 4)
	old("2028-12-01 10:00+07", "2029-01-05", 7)

	st, err := e.svc.VillageStats(e.ctx, f.manager, f.village, 90)
	if err != nil {
		t.Fatal(err)
	}
	r := st.Requests
	if r.Total != 5 || r.Pending != 1 || r.Accepted != 2 || r.Rejected != 1 || r.Expired != 1 || r.Cancelled != 1 || r.Responded != 3 {
		t.Errorf("requests = %+v", r)
	}
	if r.MedianResponse != 2*time.Hour || r.ResponseRate() != 75 || r.AcceptanceRate() != 67 {
		t.Errorf("median %v, response %d%%, acceptance %d%%", r.MedianResponse, r.ResponseRate(), r.AcceptanceRate())
	}
	if st.Visits != (booking.VisitStats{Visits: 1, Visitors: 3, Revenue: 300000}) {
		t.Errorf("visits = %+v", st.Visits)
	}
	if !st.From.Equal(booking.Date(2029, 12, 15)) || !st.To.Equal(booking.Date(2030, 3, 14)) {
		t.Errorf("period %v – %v", st.From, st.To)
	}

	if len(st.Months) != 12 || !st.Months[0].Month.Equal(booking.Date(2029, 4, 1)) || !st.Months[11].Month.Equal(booking.Date(2030, 3, 1)) {
		t.Fatalf("months = %+v", st.Months)
	}
	if st.Months[11].Visitors != 3 || st.Months[2].Visitors != 4 || st.Months[5].Visitors != 0 {
		t.Errorf("monthly visitors: March %d, June %d, September %d", st.Months[11].Visitors, st.Months[2].Visitors, st.Months[5].Visitors)
	}
	if len(st.Packages) != 1 || st.Packages[0].Requests != 5 || st.Packages[0].Visitors != 3 {
		t.Errorf("packages = %+v", st.Packages)
	}

	year, _ := e.svc.VillageStats(e.ctx, f.manager, f.village, 365)
	if year.Visits.Visitors != 7 || year.Requests.Total != 6 {
		t.Errorf("365 days: visitors %d, requests %d (the 2029-01 visit must stay out)", year.Visits.Visitors, year.Requests.Total)
	}

	// Ownership: other users, even managers of other villages, get ErrNotFound.
	other := e.fixture(5)
	if _, err := e.svc.VillageStats(e.ctx, other.manager, f.village, 90); !errors.Is(err, booking.ErrNotFound) {
		t.Errorf("other manager: %v", err)
	}
	if _, err := e.svc.VillageStats(e.ctx, traveler, f.village, 90); !errors.Is(err, booking.ErrNotFound) {
		t.Errorf("traveler: %v", err)
	}

	// An empty village gives zeros, not errors.
	empty, err := e.svc.VillageStats(e.ctx, other.manager, other.village, 30)
	if err != nil || empty.Requests.Total != 0 || empty.Requests.HasDecisions() || len(empty.Months) != 12 {
		t.Errorf("empty village: %+v %v", empty.Requests, err)
	}
}
