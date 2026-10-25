package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func reviewDurationSeconds(seconds int64) string {
	var out strings.Builder
	for _, part := range []struct {
		unit  string
		scale int64
	}{{"d", 86400}, {"h", 3600}, {"m", 60}, {"s", 1}} {
		amount := seconds / part.scale
		seconds %= part.scale
		if amount > 0 {
			out.WriteString(strconv.FormatInt(amount, 10) + part.unit)
		}
	}
	if out.Len() == 0 {
		return "0s"
	}
	return out.String()
}
func reviewDurationFraction(value int64, unit string) string {
	fraction := strings.TrimRight(fmt.Sprintf("%03d", value%1000), "0")
	if fraction == "" {
		return strconv.FormatInt(value/1000, 10) + unit
	}
	return fmt.Sprintf("%d.%s%s", value/1000, fraction, unit)
}
func reviewDurationRound(value, unit int64) int64 {
	whole, remainder := value/unit, value%unit
	if remainder >= unit/2 {
		whole++
	}
	return whole
}

// Labels are approximate presentation. Editable/request durations remain exact.
func reviewDuration(d time.Duration) string {
	if d < 0 {
		return "Invalid duration"
	}
	if d == 0 {
		return "0s"
	}
	ns := int64(d)
	switch {
	case d >= time.Minute:
		return reviewDurationSeconds(reviewDurationRound(ns, int64(time.Second)))
	case d >= time.Second:
		ms := reviewDurationRound(ns, int64(time.Millisecond))
		if ms >= 60000 {
			return reviewDurationSeconds(ms / 1000)
		}
		return reviewDurationFraction(ms, "s")
	case d >= time.Millisecond:
		us := reviewDurationRound(ns, int64(time.Microsecond))
		if us >= 1000000 {
			return "1s"
		}
		return reviewDurationFraction(us, "ms")
	case d >= time.Microsecond:
		return reviewDurationFraction(ns, "µs")
	default:
		return strconv.FormatInt(ns, 10) + "ns"
	}
}
func reviewDurationIsRounded(d time.Duration) bool {
	switch {
	case d >= time.Minute:
		return d%time.Second != 0
	case d >= time.Second:
		return d%time.Millisecond != 0
	case d >= time.Millisecond:
		return d%time.Microsecond != 0
	default:
		return false
	}
}
