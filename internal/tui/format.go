package tui

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/inferal-oss/arm/internal/model"
)

func safe(text string) string {
	var out strings.Builder
	for _, r := range text {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			switch r {
			case '\n':
				out.WriteString(`\n`)
			case '\r':
				out.WriteString(`\r`)
			case '\t':
				out.WriteString(`\t`)
			default:
				fmt.Fprintf(&out, `\u%04x`, r)
			}
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func short(text string, width int) string { return ansi.Truncate(safe(text), max(0, width), "…") }

func exact(number json.Number) string {
	text := number.String()
	if text == "" {
		return "unknown"
	}
	return safe(text)
}

func byteCount(number string) string {
	n, ok := new(big.Rat).SetString(number)
	if !ok {
		return "unknown"
	}
	abs := new(big.Rat).Abs(new(big.Rat).Set(n))
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	i := 0
	for i < len(units)-1 && abs.Cmp(big.NewRat(1024, 1)) >= 0 {
		n.Quo(n, big.NewRat(1024, 1))
		abs.Quo(abs, big.NewRat(1024, 1))
		i++
	}
	text := n.FloatString(1)
	text = strings.TrimSuffix(text, ".0")
	return text + " " + units[i]
}

func quantity(number json.Number, unit string) string {
	if unit == "bytes" {
		return byteCount(number.String())
	}
	if unit == "logical-core-equivalents" {
		unit = "cores"
	} else if unit == "socket-binding" {
		unit = "bindings"
	}
	return exact(number) + " " + safe(unit)
}

func measurement(m model.Measurement) string {
	if m.Value == nil || math.IsNaN(*m.Value) || math.IsInf(*m.Value, 0) {
		return "unavailable"
	}
	if m.Unit == "bytes" || m.Unit == "bytes/second" {
		text := byteCount(strconv.FormatFloat(*m.Value, 'f', 0, 64))
		if m.Unit == "bytes/second" {
			text += "/s"
		}
		return text
	}
	return fmt.Sprintf("%.1f%%", *m.Value)
}

func owner(uid, username string) string {
	if username != "" {
		return safe(username) + " (UID " + safe(uid) + ")"
	}
	return "UID " + safe(uid)
}

func date(value *time.Time) string {
	if value == nil {
		return "—"
	}
	return value.Local().Format("Jan 02 15:04:05 MST")
}

func historyLine(points []model.MonitorPoint, width int, cadence time.Duration, ceiling float64, value func(model.MonitorPoint) *float64) string {
	if len(points) > width {
		points = points[len(points)-width:]
	}
	const bars = "▁▂▃▄▅▆▇█"
	levels := []rune(bars)
	var out strings.Builder
	for i, point := range points {
		n := value(point)
		gap := i > 0 && (point.At.Sub(points[i-1].At) > cadence*3/2 || !point.At.After(points[i-1].At))
		if n == nil || ceiling <= 0 || math.IsNaN(*n) || math.IsInf(*n, 0) || gap {
			out.WriteRune('·')
			continue
		}
		out.WriteRune(levels[int(math.Round(max(0, min(1, *n/ceiling))*7))])
	}
	return out.String()
}

// The live account lookup is explicitly distinct from a name recorded at creation.
func (m *dashboardModel) owner(uid, username, host string) string {
	if username != "" {
		return owner(uid, username)
	}
	if host != "" && host == m.data.status.Host && uid == m.data.status.Owner && m.data.status.OwnerUsername != "" {
		return owner(uid, m.data.status.OwnerUsername) + " · current account; historical name unknown"
	}
	return owner(uid, "")
}
