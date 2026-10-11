package booking

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"desakita/internal/platform/httpx"
	"desakita/internal/platform/i18n"
	"desakita/internal/platform/render"
)

// Chart geometry for the monthly visitors column chart (SVG viewBox units). Text stays in
// HTML around the SVG; bars carry <title> tooltips and the table below holds every value.
const (
	chartW, chartH   = 720, 220 // wide chart; the compact one (phones) is chartCompactW wide
	chartCompactW    = 360
	compactMonths    = 6
	chartLeft        = 40 // room for y ticks
	chartTop         = 16
	chartBottom      = 24 // room for month labels
	chartBarMaxWidth = 24
	chartRadius      = 4
)

type chartBar struct {
	X, LabelX         float64
	Path              string // column with a rounded top, square at the baseline
	HitX, HitW        float64
	Label, Title      string
	ValueY            float64
	Value             int64
	ShowValue         bool // label only the highest and the latest month
	HitY, HitH, BaseY float64
}

type chartTick struct {
	Y     float64
	Label string
}

type statsChart struct {
	W, H, Left, Right, BaseY float64
	Bars                     []chartBar
	Ticks                    []chartTick
	Empty                    bool
}

type statsData struct {
	Village  VillageSummary
	Stats    Stats
	Periods  []int
	Chart    statsChart
	Compact  statsChart // last compactMonths months, for narrow screens
	FromText string
	ToText   string
	Median   string
}

// niceMax rounds v up to 2, 4, 6, 8 or 10 × 10ⁿ, so the 0 / half / max ticks are whole
// numbers.
func niceMax(v int64) int64 {
	for p := int64(1); ; p *= 10 {
		for _, m := range []int64{2, 4, 6, 8, 10} {
			if m*p >= v {
				return m * p
			}
		}
	}
}

func buildChart(tr *i18n.Translator, months []MonthStat, width float64) statsChart {
	c := statsChart{W: width, H: chartH, Left: chartLeft, Right: width - 8, BaseY: chartH - chartBottom}
	var top int64
	maxIdx := 0
	for i, m := range months {
		if m.Visitors > top {
			top, maxIdx = m.Visitors, i
		}
	}
	c.Empty = top == 0
	ceil := niceMax(top)
	plotH := c.BaseY - chartTop
	for i := 0; i <= 2; i++ {
		v := ceil * int64(i) / 2
		c.Ticks = append(c.Ticks, chartTick{Y: c.BaseY - plotH*float64(v)/float64(ceil), Label: strconv.FormatInt(v, 10)})
	}
	slot := (c.Right - c.Left) / float64(len(months))
	w := min(chartBarMaxWidth, slot*0.6)
	for i, m := range months {
		x := c.Left + slot*float64(i) + (slot-w)/2
		h := plotH * float64(m.Visitors) / float64(ceil)
		b := chartBar{
			X: x, LabelX: x + w/2, HitX: c.Left + slot*float64(i), HitW: slot, HitY: chartTop, HitH: plotH, BaseY: c.BaseY,
			Label: tr.T(fmt.Sprintf("date.month_short.%d", int(m.Month.Month()))),
			Title: render.FormatMonth(tr, m.Month) + ": " + tr.T("stats.visitors_count", m.Visitors),
			Value: m.Visitors, ValueY: c.BaseY - h - 6,
			ShowValue: m.Visitors > 0 && (i == maxIdx || i == len(months)-1),
		}
		if h > 0 {
			r := min(chartRadius, h, w/2)
			y := c.BaseY - h
			b.Path = fmt.Sprintf("M%.1f %.1fV%.1fQ%.1f %.1f %.1f %.1fH%.1fQ%.1f %.1f %.1f %.1fV%.1fZ",
				x, c.BaseY, y+r, x, y, x+r, y, x+w-r, x+w, y, x+w, y+r, c.BaseY)
		}
		c.Bars = append(c.Bars, b)
	}
	return c
}

func (h *Handler) stats(w http.ResponseWriter, r *http.Request) {
	viewer, _ := httpx.ViewerFrom(r.Context())
	villageID, ok := idParam(r, "villageID")
	if !ok {
		h.rd.NotFound(w, r)
		return
	}
	days := StatsPeriods[0]
	if p, err := strconv.Atoi(r.URL.Query().Get("periode")); err == nil && slices.Contains(StatsPeriods, p) {
		days = p
	}
	st, err := h.svc.VillageStats(r.Context(), viewer.ID, villageID, days)
	if errors.Is(err, ErrNotFound) {
		h.rd.NotFound(w, r)
		return
	}
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	v, err := h.catalog.Village(r.Context(), villageID)
	if err != nil {
		h.rd.ServerError(w, r, err)
		return
	}
	tr := h.tr.From(r.Context())
	d := statsData{
		Village: v, Stats: st, Periods: StatsPeriods, Chart: buildChart(tr, st.Months, chartW),
		Compact:  buildChart(tr, st.Months[len(st.Months)-compactMonths:], chartCompactW),
		FromText: render.FormatDate(tr, st.From), ToText: render.FormatDate(tr, st.To),
	}
	if st.Requests.Responded > 0 {
		d.Median = formatDuration(tr, st.Requests.MedianResponse)
	}
	h.rd.Page(w, r, http.StatusOK, "manage_stats", d)
}

// formatDuration renders a response time as "45 menit", "3 jam 20 menit" or "2 hari 4 jam".
func formatDuration(tr *i18n.Translator, d time.Duration) string {
	mins := int(d.Round(time.Minute) / time.Minute)
	switch {
	case mins < 60:
		return tr.T("stats.minutes", mins)
	case mins < 24*60:
		return tr.T("stats.hours_minutes", mins/60, mins%60)
	default:
		return tr.T("stats.days_hours", mins/(24*60), mins%(24*60)/60)
	}
}
