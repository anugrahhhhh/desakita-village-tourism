package booking

import (
	"strings"
	"testing"
	"time"

	"desakita/internal/platform/i18n"
	"desakita/web"
)

func TestNiceMax(t *testing.T) {
	for v, want := range map[int64]int64{0: 2, 1: 2, 3: 4, 7: 8, 10: 10, 11: 20, 35: 40, 99: 100, 101: 200, 1234: 2000} {
		if got := niceMax(v); got != want {
			t.Errorf("niceMax(%d) = %d, want %d", v, got, want)
		}
	}
}

func TestBuildChart(t *testing.T) {
	b, err := i18n.LoadBundle(web.FS)
	if err != nil {
		t.Fatal(err)
	}
	months := make([]MonthStat, 12)
	for i := range months {
		months[i].Month = Date(2029, time.Month(4+i), 1)
	}
	if c := buildChart(b.For("id"), months, chartW); !c.Empty || len(c.Bars) != 12 || c.Bars[0].Path != "" {
		t.Error("all-zero chart should be empty with no bar paths")
	}
	months[2].Visitors, months[11].Visitors = 40, 7
	c := buildChart(b.For("en"), months, chartW)
	if c.Empty || c.Ticks[2].Label != "40" || c.Ticks[1].Label != "20" {
		t.Errorf("ticks = %+v", c.Ticks)
	}
	top := c.Bars[2]
	if !strings.HasPrefix(top.Path, "M") || top.ValueY >= c.BaseY || !top.ShowValue || !c.Bars[11].ShowValue || c.Bars[5].ShowValue {
		t.Errorf("bars: top %+v", top)
	}
	if c.Bars[2].Label != "Jun" || !strings.Contains(c.Bars[2].Title, "June 2029: 40 visitors") {
		t.Errorf("labels: %q %q", c.Bars[2].Label, c.Bars[2].Title)
	}
	for _, bar := range c.Bars {
		if w := bar.HitW; w <= 0 || bar.HitX < c.Left || bar.HitX+w > c.Right+0.01 {
			t.Errorf("hit area outside the plot: %+v", bar)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	b, _ := i18n.LoadBundle(web.FS)
	id, en := b.For("id"), b.For("en")
	cases := []struct {
		d      time.Duration
		id, en string
	}{
		{time.Minute, "1 menit", "1 minute"},
		{45 * time.Minute, "45 menit", "45 minutes"},
		{3*time.Hour + 20*time.Minute, "3 jam 20 menit", "3 h 20 min"},
		{52 * time.Hour, "2 hari 4 jam", "2 d 4 h"},
	}
	for _, c := range cases {
		if got := formatDuration(id, c.d); got != c.id {
			t.Errorf("id %v = %q", c.d, got)
		}
		if got := formatDuration(en, c.d); got != c.en {
			t.Errorf("en %v = %q", c.d, got)
		}
	}
}
