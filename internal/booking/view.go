package booking

import (
	"time"
)

// Actions lists which transitions are currently valid for a booking. The rules mirror the
// guarded UPDATEs in queries/booking.sql; the database stays the authority.
type Actions struct {
	Confirm, Reject, Cancel, Complete bool
}

func (a Actions) Any() bool { return a.Confirm || a.Reject || a.Cancel || a.Complete }

// ActionsFor returns the transitions a manager could perform now. Travelers may only cancel
// (Actions.Cancel).
func (s *Service) ActionsFor(b Booking) Actions {
	now, today := s.cfg.Now(), s.Today()
	switch b.Status {
	case StatusPending:
		return Actions{
			Confirm: now.Before(b.ExpiresAt),
			Reject:  true,
			Cancel:  !b.StartDate.Before(today),
		}
	case StatusConfirmed:
		return Actions{
			Cancel:   !b.StartDate.Before(today),
			Complete: !b.StartDate.After(today),
		}
	}
	return Actions{}
}

// Cell is one square of a month calendar.
type Cell struct {
	Date    time.Time
	InMonth bool
	Day     Day
	// Bookable: within the traveler booking window and has seats left.
	Bookable bool
	// Editable: within the manager editing window (today..today+WindowDays).
	Editable bool
	Today    bool
}

// Month is a Monday-first calendar grid for one month.
type Month struct {
	First      time.Time // first day of the month
	Prev, Next time.Time // first days of adjacent months, zero if outside the navigable range
	Weeks      [][]Cell
}

// MonthStart parses "2006-01" (empty or invalid means the current month) and clamps it to
// the months that overlap today..today+WindowDays.
func (s *Service) MonthStart(param string) time.Time {
	today := s.Today()
	current := Date(today.Year(), today.Month(), 1)
	last := today.AddDate(0, 0, WindowDays)
	lastMonth := Date(last.Year(), last.Month(), 1)
	m, err := time.Parse("2006-01", param)
	if err != nil {
		return current
	}
	m = Date(m.Year(), m.Month(), 1)
	if m.Before(current) {
		return current
	}
	if m.After(lastMonth) {
		return lastMonth
	}
	return m
}

// MonthGrid builds the calendar for the month starting at first.
func (s *Service) MonthGrid(days []Day, first time.Time) Month {
	today := s.Today()
	bookFrom, bookTo := s.BookableRange()
	editTo := today.AddDate(0, 0, WindowDays)
	byDate := make(map[time.Time]Day, len(days))
	for _, d := range days {
		byDate[d.Date] = d
	}

	m := Month{First: first}
	if prev := first.AddDate(0, -1, 0); !prev.AddDate(0, 1, -1).Before(today) {
		m.Prev = prev
	}
	if next := first.AddDate(0, 1, 0); !next.After(editTo) {
		m.Next = next
	}

	// Back up to Monday.
	offset := (int(first.Weekday()) + 6) % 7
	d := first.AddDate(0, 0, -offset)
	for {
		week := make([]Cell, 7)
		for i := range week {
			day, ok := byDate[d]
			c := Cell{Date: d, InMonth: d.Month() == first.Month(), Today: d.Equal(today)}
			if ok {
				c.Day = day
				c.Bookable = !d.Before(bookFrom) && !d.After(bookTo) && day.Remaining() > 0
				c.Editable = !d.Before(today) && !d.After(editTo)
			}
			week[i] = c
			d = d.AddDate(0, 0, 1)
		}
		m.Weeks = append(m.Weeks, week)
		if d.Month() != first.Month() {
			break
		}
	}
	return m
}

// MonthRange returns the dates MonthGrid needs data for (the full weeks shown).
func MonthRange(first time.Time) (from, to time.Time) {
	from = first.AddDate(0, 0, -((int(first.Weekday()) + 6) % 7))
	last := first.AddDate(0, 1, -1)
	to = last.AddDate(0, 0, (7-int(last.Weekday()))%7)
	return from, to
}
