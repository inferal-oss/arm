package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/model"
	"github.com/charmbracelet/x/ansi"
)

// These are ordinary-model tests. They never start Init, a Program, a timer,
// a transport command, or a native collector. No native fixture guard is reused.
type monitorFocusTransport struct {
	t          *testing.T
	operations []string
	closes     int
}

func (s *monitorFocusTransport) forbidden(operation string) {
	s.t.Helper()
	s.operations = append(s.operations, operation)
	s.t.Errorf("unexpected monitor focus transport operation: %s", operation)
}
func (s *monitorFocusTransport) Call(_ context.Context, operation string, _, _ any) error {
	s.forbidden("Call:" + operation)
	return errors.New("monitor focus transport is inert")
}
func (s *monitorFocusTransport) Events() <-chan model.Event {
	s.forbidden("Events")
	return nil
}
func (s *monitorFocusTransport) Telemetry() <-chan model.MonitorPage {
	s.forbidden("Telemetry")
	return nil
}
func (s *monitorFocusTransport) Done() <-chan struct{} {
	s.forbidden("Done")
	return nil
}
func (s *monitorFocusTransport) Err() error {
	s.forbidden("Err")
	return errors.New("monitor focus transport is inert")
}
func (s *monitorFocusTransport) Close() { s.closes++ }

var _ transport = (*monitorFocusTransport)(nil)

var monitorFocusTime = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

func monitorFocusID(disk bool, index int) string {
	kind := "network"
	if disk {
		kind = "disk"
	}
	return fmt.Sprintf("%s-%02d-long-stable-counter-identity", kind, index)
}

func monitorFocusCopy(selection *ioSelection) ioSelection {
	copy := ioSelection{All: selection.All}
	if selection.IDs != nil {
		copy.IDs = make(map[string]bool, len(selection.IDs))
		for id, selected := range selection.IDs {
			copy.IDs[id] = selected
		}
	}
	return copy
}

func monitorFocusFixture(t *testing.T, width, height int, disk, missing bool) *dashboardModel {
	t.Helper()
	home := pendingHome(t)
	ctx, cancel := context.WithCancel(t.Context())
	m := newDashboard(ctx, home)
	s := &monitorFocusTransport{t: t}
	t.Cleanup(func() {
		s.Close()
		cancel()
		entries, err := os.ReadDir(home)
		t.Logf("monitor-focus-cleanup operations=%q closes=%d canceled=%t homeEntries=%d homeReadError=%v", s.operations, s.closes, ctx.Err() != nil, len(entries), err)
		if len(s.operations) != 0 || s.closes != 1 || ctx.Err() == nil || err != nil || len(entries) != 0 {
			t.Error("monitor focus fixture cleanup or no-effect invariant failed")
		}
	})
	if m.pendingStoreError != "" || len(m.pendingRecords) != 0 {
		t.Fatal("fresh monitor focus constructor bookkeeping")
	}
	m.session, m.connected, m.connecting = s, true, false
	m.width, m.height, m.view = width, height, 0
	m.notice = ""
	m.timeNow, m.timeMonitor, m.lastRefresh = monitorFocusTime, monitorFocusTime, monitorFocusTime
	m.data.status = model.Status{Host: "synthetic-host", Owner: "1000"}
	value := func(n float64, unit string) model.Measurement {
		return model.Measurement{Value: &n, Unit: unit, Quality: "reported", At: monitorFocusTime}
	}
	sample := &model.MonitorSnapshot{At: monitorFocusTime, CPU: value(25, "percent")}
	for index := 0; index < 64; index++ {
		sample.Cores = append(sample.Cores, model.MonitorCPU{ID: fmt.Sprintf("synthetic-cpu-%02d", index), Busy: value(float64(index), "percent")})
	}
	for index := 0; index < 24; index++ {
		for _, isDisk := range []bool{true, false} {
			item := model.MonitorIO{ID: monitorFocusID(isDisk, index), Name: fmt.Sprintf("Device %02d with a deliberately long generic counter label", index), Read: value(1024, "bytes/second"), Write: value(0, "bytes/second")}
			if isDisk {
				sample.DiskIO = append(sample.DiskIO, item)
			} else {
				sample.Network = append(sample.Network, item)
			}
		}
	}
	m.page = model.MonitorPage{Latest: sample, Epoch: "synthetic-monitor-focus", Cadence: time.Second, StaleAfter: 3 * time.Second}
	for _, isDisk := range []bool{true, false} {
		*m.ioSelection(isDisk) = ioSelection{IDs: map[string]bool{monitorFocusID(isDisk, 0): true, monitorFocusID(isDisk, 7): true}}
	}
	if missing {
		m.ioSelection(disk).IDs["missing-long-selected-counter-identity"] = true
	}
	if command := m.openIOPicker(disk); command != nil {
		t.Fatal("opening local monitor picker produced a command")
	}
	m.syncViewport()
	return m
}

func monitorFocusKey(t *testing.T, m *dashboardModel, key string) {
	t.Helper()
	if command := m.key(mouseKey(key)); command != nil {
		t.Fatalf("local monitor picker key %q produced a command; not executed", key)
	}
	m.syncViewport()
}

func monitorFocusArrange(t *testing.T, m *dashboardModel, id string) {
	t.Helper()
	for steps := 0; steps <= len(m.ioOptions()); steps++ {
		if m.ioPicker.selectedID == id {
			return
		}
		monitorFocusKey(t, m, "down")
	}
	t.Fatal("could not focus the arranged counter ID")
}

func monitorFocusObserve(t *testing.T, m *dashboardModel, stage string) {
	t.Helper()
	options := make([]map[string]any, 0, len(m.ioOptions()))
	for _, option := range m.ioOptions() {
		options = append(options, map[string]any{"id": option.id, "name": option.name, "missing": option.missing, "checked": option.checked})
	}
	// Keep complete original ordinary text before assertions, including failures.
	observation := map[string]any{
		"stage": stage, "grid": []int{m.width, m.height}, "selectedID": m.ioPicker.selectedID,
		"effectiveIndex": m.pickerIndex(), "options": options,
		"disk": monitorFocusCopy(&m.diskSelection), "network": monitorFocusCopy(&m.networkSelection),
		"offset": m.viewport.YOffset(), "viewportSize": []int{m.viewport.Width(), m.viewport.Height()},
		"pickerText": ansi.Strip(m.pickerText()), "viewportText": ansi.Strip(m.viewport.View()),
		"frame": ansi.Strip(m.View().Content),
	}
	raw, err := json.MarshalIndent(observation, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("monitor-focus-observation\n%s", raw)
}

// Inspect the actual rendered row pair, rather than mirroring the viewport's
// row arithmetic. Identity must be valid, and both label and ID must be visible.
func monitorFocusVisible(t *testing.T, m *dashboardModel) string {
	t.Helper()
	options := m.ioOptions()
	index := m.pickerIndex()
	if index < 0 || index >= len(options) {
		t.Fatal("no effective focused option")
	}
	id := options[index].id
	if m.ioPicker.selectedID != id {
		t.Errorf("orphaned focused ID %q; effective fallback is %q", m.ioPicker.selectedID, id)
	}
	idPrefix := strings.SplitN(id, "-long", 2)[0]
	visiblePair := func(text string) bool {
		lines := strings.Split(ansi.Strip(text), "\n")
		for row := 0; row+1 < len(lines); row++ {
			if strings.Contains(lines[row], "> [") && strings.Contains(lines[row+1], idPrefix) {
				return true
			}
		}
		return false
	}
	if !visiblePair(m.viewport.View()) || !visiblePair(m.View().Content) {
		t.Errorf("focused counter %q is not visible as a label/ID pair in the viewport and full frame", id)
	}
	return id
}

func monitorFocusAssertOther(t *testing.T, m *dashboardModel, disk bool, want ioSelection) {
	t.Helper()
	if got := monitorFocusCopy(m.ioSelection(!disk)); !reflect.DeepEqual(got, want) {
		t.Errorf("local picker changed the other chart: got %+v want %+v", got, want)
	}
}

func monitorFocusAssertScope(t *testing.T, m *dashboardModel, disk, all bool, want map[string]bool) {
	t.Helper()
	got := m.ioSelection(disk)
	if got.All != all {
		t.Errorf("chart All=%t want %t", got.All, all)
	}
	actual := map[string]bool{}
	for _, id := range got.selectedIDs(m.ioItems(disk)) {
		actual[id] = true
	}
	if !reflect.DeepEqual(actual, want) {
		t.Errorf("chart selection got %v want %v", actual, want)
	}
}

func TestMonitorPickerMissingFocusRemainsVisible(t *testing.T) {
	for _, grid := range [][2]int{{45, 14}, {72, 28}, {100, 35}} {
		for _, disk := range []bool{true, false} {
			for _, action := range []string{"space", "n", "a"} {
				t.Run(fmt.Sprintf("%dx%d/disk=%t/%s", grid[0], grid[1], disk, action), func(t *testing.T) {
					m := monitorFocusFixture(t, grid[0], grid[1], disk, true)
					other := monitorFocusCopy(m.ioSelection(!disk))
					monitorFocusArrange(t, m, "missing-long-selected-counter-identity")
					monitorFocusObserve(t, m, "focused-missing-before-removal")
					monitorFocusVisible(t, m)
					if m.viewport.YOffset() == 0 {
						t.Fatal("dense missing-row fixture did not scroll")
					}
					monitorFocusKey(t, m, action)
					monitorFocusObserve(t, m, "after-missing-row-removal-before-next-toggle")
					want := map[string]bool{}
					switch action {
					case "space":
						want[monitorFocusID(disk, 0)], want[monitorFocusID(disk, 7)] = true, true
					case "a":
						for _, item := range m.ioItems(disk) {
							want[item.ID] = true
						}
					}
					monitorFocusAssertScope(t, m, disk, action == "a", want)
					monitorFocusAssertOther(t, m, disk, other)
					for _, option := range m.ioOptions() {
						if option.id == "missing-long-selected-counter-identity" {
							t.Error("deselected missing option remained in the picker")
						}
					}
					// Nonfatal focus checks allow the original second-toggle outcome
					// to be recorded even when the candidate is reproduced.
					id := monitorFocusVisible(t, m)
					if want[id] {
						delete(want, id)
					} else {
						want[id] = true
					}
					monitorFocusKey(t, m, "space")
					monitorFocusObserve(t, m, "after-next-toggle")
					monitorFocusAssertScope(t, m, disk, false, want)
					monitorFocusAssertOther(t, m, disk, other)
					monitorFocusVisible(t, m)
				})
			}
		}
	}
}

func TestMonitorPickerResizeKeepsFocusVisible(t *testing.T) {
	for _, grid := range [][2]int{{72, 28}, {100, 35}} {
		for _, disk := range []bool{true, false} {
			t.Run(fmt.Sprintf("%dx%d-to-45x14/disk=%t", grid[0], grid[1], disk), func(t *testing.T) {
				m := monitorFocusFixture(t, grid[0], grid[1], disk, false)
				other := monitorFocusCopy(m.ioSelection(!disk))
				before := monitorFocusCopy(m.ioSelection(disk))
				id := monitorFocusID(disk, 8)
				monitorFocusArrange(t, m, id)
				monitorFocusObserve(t, m, "focused-before-resize")
				monitorFocusVisible(t, m)
				original, command := m.Update(tea.WindowSizeMsg{Width: 45, Height: 14})
				if original != m || command != nil {
					t.Fatal("original resize replaced the model or produced a command; not executed")
				}
				// Update owns its wall-clock cut; fixed observation time here is
				// arrangement for ordinary rendering, not a replacement clock.
				m.timeNow = monitorFocusTime
				m.syncViewport()
				monitorFocusObserve(t, m, "after-resize-before-toggle")
				if m.ioPicker.selectedID != id || !reflect.DeepEqual(monitorFocusCopy(m.ioSelection(disk)), before) {
					t.Error("resize changed focus identity or local chart scope")
				}
				monitorFocusVisible(t, m)
				monitorFocusKey(t, m, "space")
				monitorFocusObserve(t, m, "after-resize-toggle")
				want := map[string]bool{monitorFocusID(disk, 0): true, monitorFocusID(disk, 7): true, id: true}
				monitorFocusAssertScope(t, m, disk, false, want)
				monitorFocusAssertOther(t, m, disk, other)
				monitorFocusVisible(t, m)
			})
		}
	}
}
