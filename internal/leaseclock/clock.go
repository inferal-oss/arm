// Package leaseclock provides boot-scoped, suspend-inclusive lease deadlines.
// Wall timestamps are audit/display data; they do not grant elapsed entitlement.
package leaseclock

import (
	"fmt"
	"math"
	"time"
)

type Sample struct {
	Boot string    `json:"boot"`
	Tick int64     `json:"tick_ns"`
	Wall time.Time `json:"wall"`
}

type Deadline struct {
	Version int       `json:"version"`
	Boot    string    `json:"boot"`
	Anchor  int64     `json:"anchor_ns"`
	End     int64     `json:"end_ns"`
	Wall    time.Time `json:"wall_expires"`
}

func (s Sample) Valid() bool {
	return s.Boot != "" && len(s.Boot) <= 256 && s.Tick >= 0 && !s.Wall.IsZero() && s.Wall.Year() >= 0 && s.Wall.Year() <= 9999
}

func New(s Sample, ttl time.Duration) (*Deadline, error) {
	if !s.Valid() || ttl <= 0 || ttl > 7*24*time.Hour || s.Tick > math.MaxInt64-int64(ttl) {
		return nil, fmt.Errorf("invalid lease clock or duration")
	}
	wallEnd := s.Wall.Add(ttl)
	if wallEnd.IsZero() || wallEnd.Year() < 0 || wallEnd.Year() > 9999 {
		return nil, fmt.Errorf("lease audit deadline cannot be persisted")
	}
	return &Deadline{Version: 1, Boot: s.Boot, Anchor: s.Tick, End: s.Tick + int64(ttl), Wall: wallEnd}, nil
}

// Remaining never rebases an old deadline onto a new boot or recovered clock.
func (d *Deadline) Remaining(s Sample, wall *time.Time) (time.Duration, error) {
	if d == nil || d.Version != 1 || d.Wall.IsZero() || !s.Valid() || d.Boot != s.Boot || d.Anchor < 0 || d.End <= d.Anchor || d.End-d.Anchor > int64(7*24*time.Hour) || s.Tick < d.Anchor || wall == nil || !d.Wall.Equal(*wall) {
		return 0, fmt.Errorf("lease elapsed deadline provenance is unavailable")
	}
	if s.Tick >= d.End {
		return 0, nil
	}
	return time.Duration(d.End - s.Tick), nil
}

func tick(sec, nsec int64) (int64, error) {
	if sec < 0 || nsec < 0 || nsec >= 1e9 || sec > (math.MaxInt64-nsec)/1e9 {
		return 0, fmt.Errorf("native elapsed clock out of range")
	}
	return sec*1e9 + nsec, nil
}
