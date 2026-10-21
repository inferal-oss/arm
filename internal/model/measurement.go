package model

import "time"

// A nil value means unavailable, never zero. Quality describes the named scope,
// not the accuracy of a different quantity such as exclusive physical extents.
type Measurement struct {
	Value    *float64  `json:"value"`
	Unit     string    `json:"unit"`
	Scope    string    `json:"scope"`
	Method   string    `json:"method"`
	Quality  string    `json:"quality"`
	At       time.Time `json:"at"`
	Complete bool      `json:"complete"`
	Detail   string    `json:"detail,omitempty"`
}

type ResourceMeasurement struct {
	Total      Measurement `json:"total"`
	Available  Measurement `json:"available"`
	Used       Measurement `json:"used"`
	Filesystem *Filesystem `json:"filesystem,omitempty"`
}

type Filesystem struct {
	Path            string      `json:"path"`
	Type            string      `json:"type"`
	Volume          string      `json:"volume"`
	Pool            string      `json:"pool"`
	IdentityQuality string      `json:"identity_quality"`
	Mount           string      `json:"mount,omitempty"`
	Shared          bool        `json:"shared"`
	PoolTotal       Measurement `json:"pool_total"`
	PoolFree        Measurement `json:"pool_free"`
	Detail          string      `json:"detail,omitempty"`
}

type StorageMeasurement struct {
	Path             string              `json:"path"`
	Filesystem       ResourceMeasurement `json:"filesystem"`
	Logical          Measurement         `json:"logical"`
	Allocated        Measurement         `json:"allocated"`
	Exclusive        Measurement         `json:"exclusive"`
	Reclaimable      Measurement         `json:"reclaimable"`
	Entries          int                 `json:"entries"`
	HardLinksSkipped int                 `json:"hard_links_skipped"`
	SymlinksSkipped  int                 `json:"symlinks_skipped"`
	MountsSkipped    int                 `json:"mounts_skipped"`
}
