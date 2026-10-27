package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"getarm.org/internal/client"
	"getarm.org/internal/model"
	"golang.org/x/sys/unix"
)

func (a *app) monitorCommand(args []string) error {
	f := a.flags("monitor")
	var q model.MonitorQuery
	f.StringVar(&q.Host, "host", "localhost", "host ID or localhost")
	f.StringVar(&q.Epoch, "epoch", "", "collector epoch for resuming a telemetry cursor")
	f.Int64Var(&q.After, "after", 0, "resume after this telemetry sequence (requires --epoch)")
	follow := f.Bool("follow", false, "stream sample pages, reconnecting with the last emitted cursor")
	f.Bool("json", false, "force JSON output (follow emits JSON lines)")
	if err := a.parse(f, args, false); err != nil {
		return err
	}
	if q.After < 0 || (q.After > 0 && q.Epoch == "") {
		return &argumentError{err: fmt.Errorf("--after requires --epoch and must be nonnegative"), command: "monitor"}
	}
	if !*follow {
		var page model.MonitorPage
		if err := a.call("monitor", q, &page); err != nil {
			return err
		}
		return a.printMonitor(page)
	}
	backoff := 200 * time.Millisecond
	reconnecting := false
	var history []model.MonitorPoint
	epoch := ""
	for {
		var outputErr error
		err := client.WatchMonitor(a.ctx, a.home, q, func(page model.MonitorPage) error {
			if reconnecting {
				a.notice("Host monitoring reconnected")
				reconnecting = false
			}
			if a.machine() {
				outputErr = json.NewEncoder(a.out).Encode(page)
			} else {
				if epoch != page.Epoch || page.Gap {
					history = nil
				}
				for _, p := range page.History {
					if len(history) == 0 || p.Sequence > history[len(history)-1].Sequence {
						history = append(history, p)
					}
				}
				if len(history) > 60 {
					history = history[len(history)-60:]
				}
				epoch = page.Epoch
				page.History = history
				outputErr = a.printMonitor(page)
			}
			if outputErr != nil {
				return outputErr
			}
			q.Epoch, q.After = page.Epoch, page.Cursor
			backoff = 200 * time.Millisecond
			return nil
		})
		if outputErr != nil {
			return outputErr
		}
		if a.ctx.Err() != nil {
			return a.ctx.Err()
		}
		var api *model.Error
		if errors.As(err, &api) && api.Code != "monitor_busy" && api.Code != "backend_stopping" {
			return err
		}
		if !reconnecting {
			a.notice("Host monitoring disconnected; reconnecting with the last emitted cursor")
			reconnecting = true
		}
		select {
		case <-a.ctx.Done():
			return a.ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(5*time.Second, backoff*2)
	}
}
func monitorNumber(m model.Measurement) string {
	if m.Value == nil {
		return "unknown"
	}
	if m.Unit == "bytes" || m.Unit == "bytes/second" {
		text := humanBytes(fmt.Sprintf("%.0f", *m.Value))
		if m.Unit == "bytes/second" {
			text += "/s"
		}
		return text
	}
	return fmt.Sprintf("%.1f%%", *m.Value)
}
func monitorSparkline(values []*float64, ceiling float64) string {
	const bars = "▁▂▃▄▅▆▇█"
	levels := []rune(bars)
	var b strings.Builder
	for _, p := range values {
		if p == nil || ceiling <= 0 || math.IsNaN(*p) || math.IsInf(*p, 0) {
			b.WriteRune('·')
			continue
		}
		i := int(math.Round(max(0, min(1, *p/ceiling)) * 7))
		b.WriteRune(levels[i])
	}
	return b.String()
}
func (a *app) printMonitor(page model.MonitorPage) error {
	if a.machine() {
		return a.print(page)
	}
	var b strings.Builder
	width := 80
	if fd, ok := a.out.(interface{ Fd() uintptr }); ok {
		if size, e := unix.IoctlGetWinsize(int(fd.Fd()), unix.TIOCGWINSZ); e == nil && size.Col > 0 {
			width = int(size.Col)
		}
	}
	graphWidth := max(4, min(60, width-35))
	columns := max(1, min(4, width/20))
	fmt.Fprintln(&b, a.paint("1;36", "ARM · Host monitor"))
	state, color := "live", "32"
	if page.Stale {
		state, color = "stale / waiting for samples", "33"
	}
	fmt.Fprintf(&b, "Host %s  ·  %s  ·  every %s  ·  age %s\n", terminalSafe(page.Host), a.paint(color, state), page.Cadence, page.Age.Round(time.Millisecond))
	fmt.Fprintln(&b, "Physical observations · reservation budgets and owners: arm resources")
	if page.Gap {
		fmt.Fprintln(&b, a.paint("33", "History gap or collector restart; the retained window replaces the previous history."))
	}
	if page.Latest == nil {
		fmt.Fprintln(&b, "\nThe collector is starting. No usage is inferred.")
		_, err := io.WriteString(a.out, b.String())
		return err
	}
	v := page.Latest
	cpu := make([]*float64, 0, len(page.History))
	mem := make([]*float64, 0, len(page.History))
	for _, p := range page.History {
		cpu = append(cpu, p.CPU)
		mem = append(mem, p.MemoryUsed)
	}
	if len(cpu) > graphWidth {
		cpu = cpu[len(cpu)-graphWidth:]
		mem = mem[len(mem)-graphWidth:]
	}
	fmt.Fprintf(&b, "\n%-8s %-25s %s\n", "CPU", monitorNumber(v.CPU), a.paint("36", monitorSparkline(cpu, 100)))
	for i, c := range v.Cores {
		fmt.Fprintf(&b, "  %-8s %7s", terminalSafe(c.ID), monitorNumber(c.Busy))
		if (i+1)%columns == 0 || i == len(v.Cores)-1 {
			fmt.Fprintln(&b)
		}
	}
	total := float64(0)
	if v.Memory.Total.Value != nil {
		total = *v.Memory.Total.Value
	}
	fmt.Fprintf(&b, "\n%-8s %-25s %s\n", "Memory", monitorNumber(v.Memory.Used)+" / "+monitorNumber(v.Memory.Total), a.paint("36", monitorSparkline(mem, total)))
	availability := "Available"
	if v.Memory.Available.Scope == "host-physical-memory-free-pages-only" {
		availability = "Free pages"
		fmt.Fprintln(&b, "       Nonfree memory includes cache; this is not application-private use or memory pressure.")
	}
	if v.Memory.Available.Quality == "estimated" {
		availability = "Available (estimated)"
	}
	fmt.Fprintf(&b, "       %s: %s\n", availability, monitorNumber(v.Memory.Available))
	fmt.Fprintf(&b, "Swap   %s / %s\n", monitorNumber(v.Swap.Used), monitorNumber(v.Swap.Total))
	fmt.Fprintf(&b, "Disk   %s available / %s · sampled %s\n", monitorNumber(v.Filesystem.Available), monitorNumber(v.Filesystem.Total), v.Filesystem.Available.At.Format(time.RFC3339))
	if v.FilesystemStale {
		fmt.Fprintln(&b, a.paint("33", "       Filesystem reading is stale or unavailable."))
	}
	if v.Filesystem.Filesystem != nil {
		fs := v.Filesystem.Filesystem
		fmt.Fprintf(&b, "       %s · %s\n", terminalSafe(fs.Type), terminalSafe(fs.Path))
		if fs.Shared {
			fmt.Fprintln(&b, "       Shared capacity; volume totals and file allocations are not exclusive or reclaimable bytes.")
		}
	}
	for _, group := range []struct {
		title, quality, detail, left, right string
		rows                                []model.MonitorIO
	}{{"Disk I/O", v.DiskQuality, v.DiskDetail, "READ", "WRITE", v.DiskIO}, {"Network", v.NetworkQuality, v.NetworkDetail, "RECEIVE", "SEND", v.Network}} {
		fmt.Fprintln(&b, "\n"+a.paint("1", group.title)+" · "+terminalSafe(group.quality))
		rows := append([]model.MonitorIO{}, group.rows...)
		rate := func(row model.MonitorIO) float64 {
			total := float64(0)
			if row.Read.Value != nil {
				total += *row.Read.Value
			}
			if row.Write.Value != nil {
				total += *row.Write.Value
			}
			return total
		}
		sort.Slice(rows, func(i, j int) bool {
			a, b := rate(rows[i]), rate(rows[j])
			if a != b {
				return a > b
			}
			return rows[i].Name < rows[j].Name
		})
		if len(rows) > 8 {
			fmt.Fprintf(&b, "Showing 8 of %d sources by activity; --json includes every source.\n", len(rows))
			rows = rows[:8]
		}
		tw := tabwriter.NewWriter(&b, 0, 4, 3, ' ', 0)
		fmt.Fprintf(tw, "SOURCE\t%s\t%s\tINTERVAL\n", group.left, group.right)
		for _, r := range rows {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", terminalSafe(r.Name), monitorNumber(r.Read), monitorNumber(r.Write), r.Interval.Round(time.Millisecond))
		}
		tw.Flush()
		if group.quality != "reported" {
			fmt.Fprintln(&b, terminalSafe(group.detail))
		}
	}
	fmt.Fprintln(&b, "\nGPU    "+monitorNumber(v.GPU)+" · "+terminalSafe(v.GPU.Detail))
	fmt.Fprintln(&b, "\nIntervals show observed rates. Unknown/reset/gapped counters are not zero. Device layers and virtual interfaces can overlap.")
	_, err := io.WriteString(a.out, b.String())
	return err
}
