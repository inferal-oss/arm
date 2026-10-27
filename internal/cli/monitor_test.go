package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/model"
)

func TestMonitorRenderingHistoryGapsUnitsAndSafeLabels(t *testing.T) {
	var out bytes.Buffer
	a := app{out: &out, outputTTY: true, colors: true}
	percent, bytesValue := 50.0, 1073741824.0
	metric := model.Measurement{Value: &bytesValue, Unit: "bytes"}
	page := model.MonitorPage{Host: "host\x1b[2J", Gap: true, Stale: true, Cadence: 2 * time.Second, Latest: &model.MonitorSnapshot{CPU: model.Measurement{Value: &percent, Unit: "percent"}, Memory: model.ResourceMeasurement{Total: metric, Used: metric}, DiskIO: []model.MonitorIO{{Name: "disk\nforged", Read: metric}}, NetworkQuality: "unavailable", GPU: model.Measurement{Detail: "unsupported"}}, History: []model.MonitorPoint{{CPU: nil}, {CPU: &percent}}}
	if err := a.printMonitor(page); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"stale", "History gap", "1 GiB", "50.0%", `host\u001b[2J`, `disk\nforged`, "unknown", "Physical observations"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q: %s", want, &out)
		}
	}
	if strings.Contains(out.String(), "\x1b[2J") {
		t.Fatal("terminal control was executed")
	}
	if got := monitorSparkline([]*float64{nil, &percent}, 100); got != "·▅" {
		t.Fatal("missing history was plotted as zero", got)
	}
	out.Reset()
	a.forceJSON = true
	if err := a.printMonitor(page); err != nil || !json.Valid(out.Bytes()) || bytes.Contains(out.Bytes(), []byte("\x1b")) {
		t.Fatal(err, &out)
	}
}
