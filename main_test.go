package main

import (
	"path/filepath"
	"testing"
)

func TestEmptyDataHasNoInitialBusinessData(t *testing.T) {
	d := emptyData()
	if len(d.Archives) != 0 {
		t.Fatalf("expected no archives, got %d", len(d.Archives))
	}
	if len(d.Months) != 1 {
		t.Fatalf("expected only structural current month, got %d months", len(d.Months))
	}
	m := d.Months[d.CurrentMonth]
	if m == nil || len(m.Items) != 0 {
		t.Fatalf("expected zero initial items")
	}
}

func TestNextMonthCarriesClosingIntoOpening(t *testing.T) {
	d := emptyData()
	key := d.CurrentMonth
	d.Months[key].Items = []Item{{
		Name: "PROVA", Opening: 10, Load: 5,
		Unloads: map[string]float64{"x": 3},
	}}
	oldData, oldSave := appData, saveTo
	defer func() { appData, saveTo = oldData, oldSave }()
	appData = d
	saveTo = filepath.Join(t.TempDir(), "data.json")

	base, err := parseMonth(key)
	if err != nil {
		t.Fatal(err)
	}
	target := nextMonth(base).Format("2006-01")
	if err := ensureMonthLocked(target); err != nil {
		t.Fatal(err)
	}
	got := appData.Months[target].Items[0].Opening
	if got != 12 {
		t.Fatalf("expected opening 12 in next month, got %v", got)
	}
	if appData.Months[target].Items[0].Load != 0 || len(appData.Months[target].Items[0].Unloads) != 0 {
		t.Fatalf("new month must start with zero load and unloads")
	}
}
