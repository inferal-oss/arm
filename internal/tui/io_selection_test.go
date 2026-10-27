package tui

import (
	"math"
	"reflect"
	"testing"

	"getarm.org/internal/model"
)

func rateItem(id string, read, write *float64) model.MonitorIO {
	return model.MonitorIO{ID: id, Read: model.Measurement{Value: read}, Write: model.Measurement{Value: write}}
}
func TestCombinedSelectedRatesUnknownAndTopologySemantics(t *testing.T) {
	a, b, c := 10.0, 20.0, 30.0
	items := []model.MonitorIO{rateItem("a", &a, &b), rateItem("b", &b, &c)}
	all := ioSelection{All: true}
	if got := all.latest(items, true); got == nil || *got != 30 {
		t.Fatal("complete sum", got)
	}
	unknown := append([]model.MonitorIO(nil), items...)
	unknown[1].Read.Value = nil
	if all.latest(unknown, true) != nil {
		t.Fatal("partial unknown silently omitted")
	}
	if got := all.latest(unknown, false); got == nil || *got != 50 {
		t.Fatal("independent direction lost", got)
	}
	for _, bad := range []float64{-1, math.NaN(), math.Inf(1)} {
		unknown[1].Read.Value = &bad
		if all.latest(unknown, true) != nil {
			t.Fatal("bad rate accepted", bad)
		}
	}
	if all.latest(nil, true) != nil {
		t.Fatal("empty inventory fabricated zero")
	}
	if all.latest(append(items, items[0]), true) != nil {
		t.Fatal("duplicate scope accepted")
	}
	history := []model.MonitorHistoryIO{{ID: "a", Read: &a, Write: &b}, {ID: "b", Read: &b, Write: &c}}
	if got := all.history(items, history, true); got == nil || *got != 30 {
		t.Fatal("history sum", got)
	}
	history[1].Read = nil
	if all.history(items, history, true) != nil {
		t.Fatal("reset/unknown history counter fabricated zero")
	}
	if got := all.history(items, history, false); got == nil || *got != 50 {
		t.Fatal("history direction availability coupled", got)
	}
	history[1].Read = &b
	if all.history(items, history[:1], true) != nil || all.history(items, append(history, model.MonitorHistoryIO{ID: "old", Read: &c}), true) != nil {
		t.Fatal("topology gap hidden")
	}
	custom := ioSelection{IDs: map[string]bool{"a": true}}
	if got := custom.history(items, history, true); got == nil || *got != 10 {
		t.Fatal("custom fixed scope lost", got)
	}
	custom.IDs["missing"] = true
	if custom.latest(items, true) != nil || custom.history(items, history, true) != nil {
		t.Fatal("missing selected device dropped")
	}
	none := ioSelection{}
	if none.latest(items, true) != nil {
		t.Fatal("none fabricated zero")
	}
	overflow := math.MaxFloat64
	huge := []model.MonitorIO{rateItem("a", &overflow, &a), rateItem("b", &overflow, &b)}
	if all.latest(huge, true) != nil {
		t.Fatal("overflow not unknown")
	}
}
func TestAllTracksNewDevicesCustomDoesNotBroaden(t *testing.T) {
	v := 0.0
	items := []model.MonitorIO{rateItem("a", &v, &v), rateItem("b", &v, &v)}
	s := ioSelection{All: true}
	s.toggle("a", items)
	if s.All || !reflect.DeepEqual(s.selectedIDs(items), []string{"b"}) {
		t.Fatal("toggle failed", s)
	}
	added := append(items, rateItem("new", &v, &v))
	if !reflect.DeepEqual(s.selectedIDs(added), []string{"b"}) {
		t.Fatal("custom scope silently broadened")
	}
	s = ioSelection{All: true}
	if len(s.selectedIDs(added)) != 3 {
		t.Fatal("all did not track new IDs")
	}
	if sum := s.latest(added, true); sum == nil || *sum != 0 {
		t.Fatal("known idle differs from unavailable", sum)
	}
}

func TestCombinedRatesRespectUnavailableQualityPerDirection(t *testing.T) {
	value := 12.0
	items := []model.MonitorIO{rateItem("one", &value, &value)}
	items[0].Read.Quality = "unavailable"
	all := ioSelection{All: true}
	if all.latest(items, true) != nil {
		t.Fatal("unavailable numeric value became total")
	}
	if got := all.latest(items, false); got == nil || *got != 12 {
		t.Fatal("independent reported direction lost", got)
	}
}
