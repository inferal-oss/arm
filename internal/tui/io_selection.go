package tui

import (
	"math"
	"sort"

	"github.com/inferal-oss/arm/internal/model"
)

// All tracks newly reported IDs; a custom set never silently broadens. Missing
// selected IDs remain part of its scope and make the aggregate unavailable.
type ioSelection struct {
	All bool
	IDs map[string]bool
}

func (s ioSelection) selectedIDs(items []model.MonitorIO) []string {
	var ids []string
	if s.All {
		for _, item := range items {
			ids = append(ids, item.ID)
		}
	} else {
		for id, selected := range s.IDs {
			if selected {
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	return ids
}
func (s *ioSelection) toggle(id string, items []model.MonitorIO) {
	if s.All {
		s.IDs = map[string]bool{}
		for _, item := range items {
			s.IDs[item.ID] = true
		}
		s.All = false
	}
	if s.IDs == nil {
		s.IDs = map[string]bool{}
	}
	if s.IDs[id] {
		delete(s.IDs, id)
	} else {
		s.IDs[id] = true
	}
}
func validRate(value *float64) bool {
	return value != nil && *value >= 0 && !math.IsNaN(*value) && !math.IsInf(*value, 0)
}
func aggregateRates(ids []string, values map[string][]*float64) *float64 {
	if len(ids) == 0 {
		return nil
	}
	total := 0.0
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			return nil
		}
		seen[id] = true
		rates := values[id]
		if len(rates) != 1 || !validRate(rates[0]) {
			return nil
		}
		total += *rates[0]
		if math.IsInf(total, 0) {
			return nil
		}
	}
	return &total
}
func (s ioSelection) latest(items []model.MonitorIO, read bool) *float64 {
	values := map[string][]*float64{}
	for _, item := range items {
		measure := item.Write
		if read {
			measure = item.Read
		}
		value := measure.Value
		if measure.Quality == "unavailable" {
			value = nil
		}
		values[item.ID] = append(values[item.ID], value)
	}
	return aggregateRates(s.selectedIDs(items), values)
}
func (s ioSelection) history(items []model.MonitorIO, points []model.MonitorHistoryIO, read bool) *float64 {
	ids := s.selectedIDs(items)
	if s.All {
		// A topology change cannot be plotted as a throughput change. Each point in
		// All mode must describe exactly the current reported membership.
		if len(points) != len(ids) {
			return nil
		}
		current := map[string]bool{}
		for _, id := range ids {
			current[id] = true
		}
		for _, point := range points {
			if !current[point.ID] {
				return nil
			}
		}
	}
	values := map[string][]*float64{}
	for _, point := range points {
		value := point.Write
		if read {
			value = point.Read
		}
		values[point.ID] = append(values[point.ID], value)
	}
	return aggregateRates(ids, values)
}
