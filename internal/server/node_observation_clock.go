package server

import (
	"encoding/json"
	"time"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/model"
)

// Receipt wall times are display only. Native boot-scoped elapsed time includes
// suspension and never rebases an old observation after boot change/regression.
func nodeObservationElapsed(anchor, now leaseclock.Sample) (time.Duration, bool) {
	if !anchor.Valid() || !now.Valid() || anchor.Boot != now.Boot || now.Tick < anchor.Tick {
		return 0, false
	}
	return time.Duration(now.Tick - anchor.Tick), true
}

func nodeCachedFreshness(at time.Time, anchor, now leaseclock.Sample, failed bool) (model.NodeReadFreshness, time.Duration) {
	f := model.NodeReadFreshness{Stale: true, StaleAfter: nodeReadTTL}
	if at.IsZero() {
		return f, -1
	}
	stamp := at.UTC()
	f.Received, f.Known = &stamp, true
	elapsed, ok := nodeObservationElapsed(anchor, now)
	if !ok {
		f.Age = -1
		return f, -1
	}
	f.Age, f.Stale = elapsed, failed || elapsed > nodeReadTTL
	return f, elapsed
}

// Anchor before request admission, conservatively including the complete RTT
// in cache/native-inventory age. No clock continuity means no fresh cache write.
func readNodeObservation(read func() (leaseclock.Sample, error), call func() (json.RawMessage, error)) (json.RawMessage, time.Time, leaseclock.Sample, error) {
	anchor, err := read()
	if err != nil || !anchor.Valid() {
		return nil, time.Time{}, leaseclock.Sample{}, model.Err("observation_clock_unavailable", "native observation clock is unavailable")
	}
	raw, err := call()
	if err != nil {
		return raw, time.Time{}, leaseclock.Sample{}, err
	}
	received, err := read()
	elapsed, ok := nodeObservationElapsed(anchor, received)
	if err != nil || !ok {
		return nil, time.Time{}, leaseclock.Sample{}, model.Err("observation_clock_unavailable", "native observation clock continuity changed during read")
	}
	if elapsed > nodeReadTimeout {
		return nil, time.Time{}, leaseclock.Sample{}, model.Err("observation_timeout", "native observation deadline elapsed during read")
	}
	return raw, received.Wall.UTC(), anchor, nil
}
