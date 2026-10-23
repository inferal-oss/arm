//go:build arm_devtrace

package manager

import (
	"os"
	"testing"

	"github.com/yrashk/arm/internal/sharelease"
)

type publicationCapacityState struct {
	Rows     int   `json:"rows"`
	Bytes    int64 `json:"bytes"`
	Reserved int   `json:"reserved"`
}
type publicationCapacityCase struct {
	Before   publicationCapacityState `json:"before"`
	Event    int                      `json:"event"`
	Old      int                      `json:"old"`
	New      int                      `json:"new"`
	Accepted bool                     `json:"accepted"`
	After    publicationCapacityState `json:"after"`
}

func TestSharePublicationFormalCapacity(t *testing.T) {
	output := os.Getenv("ARM_PUBLICATION_CAPACITY")
	if output == "" {
		t.Fatal("capacity observations output required")
	}
	capacities := []sharelease.JournalCapacity{
		{},
		{Rows: sharelease.MaxJournalRows - 8, Bytes: sharelease.MaxJournalBytes - 8*sharelease.MaxJournalRecordBytes, ReservedSlots: 8},
		{Rows: sharelease.MaxJournalRows, Bytes: sharelease.MaxJournalBytes},
		{Bytes: sharelease.MaxJournalBytes - 1},
		{Rows: sharelease.MaxJournalRows - 1},
		{Rows: 1, Bytes: sharelease.MaxJournalBytes - 8*sharelease.MaxJournalRecordBytes, ReservedSlots: 8},
	}
	project := func(c sharelease.JournalCapacity) publicationCapacityState {
		return publicationCapacityState{c.Rows, c.Bytes, c.ReservedSlots}
	}
	var observations []publicationCapacityCase
	for _, before := range capacities {
		if err := before.Validate(); err != nil {
			t.Fatal("fixture itself is invalid", err)
		}
		for _, event := range []int{0, 1, sharelease.MaxJournalRecordBytes, sharelease.MaxJournalRecordBytes + 1} {
			for _, old := range []int{0, before.ReservedSlots, before.ReservedSlots + 1} {
				for _, newSlots := range []int{0, max(0, old-1), sharelease.PublicationFinalSlots, sharelease.PublicationFinalSlots + 1} {
					after, err := before.Change(event, old, newSlots)
					observations = append(observations, publicationCapacityCase{Before: project(before), Event: event, Old: old, New: newSlots, Accepted: err == nil, After: project(after)})
				}
			}
		}
	}
	if len(observations) != 288 {
		t.Fatal("capacity roster incomplete", len(observations))
	}
	if err := formalWriteJSON(output, observations); err != nil {
		t.Fatal(err)
	}
}
