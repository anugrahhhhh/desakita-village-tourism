package booking

import (
	"testing"
	"time"
)

func testService(now time.Time) *Service {
	loc, _ := time.LoadLocation("Asia/Jakarta")
	return &Service{cfg: Config{PendingTTL: 48 * time.Hour, Location: loc, Now: func() time.Time { return now }}}
}

func TestActionsFor(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Jakarta")
	now := time.Date(2030, 3, 10, 10, 0, 0, 0, loc)
	s := testService(now)
	future, today, past := Date(2030, 3, 15), Date(2030, 3, 10), Date(2030, 3, 9)
	later := now.Add(time.Hour)

	cases := []struct {
		name string
		b    Booking
		want Actions
	}{
		{"pending", Booking{Status: StatusPending, StartDate: future, ExpiresAt: later}, Actions{Confirm: true, Reject: true, Cancel: true}},
		{"pending expired", Booking{Status: StatusPending, StartDate: future, ExpiresAt: now}, Actions{Reject: true, Cancel: true}},
		{"confirmed future", Booking{Status: StatusConfirmed, StartDate: future}, Actions{Cancel: true}},
		{"confirmed today", Booking{Status: StatusConfirmed, StartDate: today}, Actions{Cancel: true, Complete: true}},
		{"confirmed past", Booking{Status: StatusConfirmed, StartDate: past}, Actions{Complete: true}},
		{"rejected", Booking{Status: StatusRejected, StartDate: future}, Actions{}},
		{"completed", Booking{Status: StatusCompleted, StartDate: past}, Actions{}},
	}
	for _, tc := range cases {
		if got := s.ActionsFor(tc.b); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestMonthGrid(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Jakarta")
	s := testService(time.Date(2030, 3, 10, 10, 0, 0, 0, loc)) // Sunday

	first := s.MonthStart("")
	if !first.Equal(Date(2030, 3, 1)) {
		t.Fatalf("current month = %v", first)
	}
	if got := s.MonthStart("2029-12"); !got.Equal(first) {
		t.Errorf("past month not clamped: %v", got)
	}
	if got := s.MonthStart("2031-01"); !got.Equal(Date(2030, 6, 1)) {
		t.Errorf("far month not clamped to June: %v", got)
	}

	from, to := MonthRange(first)
	if !from.Equal(Date(2030, 2, 25)) || !to.Equal(Date(2030, 3, 31)) {
		t.Fatalf("range %v..%v", from, to) // March 2030 starts on Friday, ends on Sunday
	}
	var days []Day
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		days = append(days, Day{Date: d, Quota: 5})
	}
	days[len(days)-1].Reserved = 5 // 03-31 sold out

	m := s.MonthGrid(days, first)
	if len(m.Weeks) != 5 || m.Weeks[0][0].Date.Weekday() != time.Monday {
		t.Fatalf("weeks = %d, first cell %v", len(m.Weeks), m.Weeks[0][0].Date.Weekday())
	}
	if !m.Prev.IsZero() || !m.Next.Equal(Date(2030, 4, 1)) {
		t.Errorf("prev=%v next=%v", m.Prev, m.Next)
	}
	cell := func(d time.Time) Cell {
		for _, w := range m.Weeks {
			for _, c := range w {
				if c.Date.Equal(d) {
					return c
				}
			}
		}
		t.Fatalf("no cell %v", d)
		return Cell{}
	}
	if c := cell(Date(2030, 3, 11)); c.Bookable || !c.Editable {
		t.Errorf("tomorrow: bookable=%v editable=%v", c.Bookable, c.Editable)
	}
	if c := cell(Date(2030, 3, 12)); !c.Bookable {
		t.Error("day after tomorrow should be bookable")
	}
	if c := cell(Date(2030, 3, 31)); c.Bookable {
		t.Error("sold-out day bookable")
	}
	if c := cell(Date(2030, 3, 9)); c.Editable || c.Bookable {
		t.Error("past day editable or bookable")
	}
	if c := cell(Date(2030, 2, 25)); c.InMonth {
		t.Error("February day marked in month")
	}
	if !cell(Date(2030, 3, 10)).Today {
		t.Error("today not marked")
	}

	june := s.MonthGrid(nil, Date(2030, 6, 1))
	if !june.Next.IsZero() || june.Prev.IsZero() {
		t.Errorf("June nav prev=%v next=%v", june.Prev, june.Next)
	}
}
