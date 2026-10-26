package model

import "time"

// Monitor samples are ephemeral physical observations, never reservation or
// enforcement evidence. Their cursor is scoped to one collector epoch.
type MonitorCPU struct {
	ID   string      `json:"id"`
	Busy Measurement `json:"busy_percent"`
}
type MonitorIO struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Read     Measurement   `json:"read_bytes_per_second"`
	Write    Measurement   `json:"write_bytes_per_second"`
	Interval time.Duration `json:"interval_ns"`
}
type MonitorSnapshot struct {
	At              time.Time           `json:"at"`
	Collection      time.Duration       `json:"collection_ns"`
	CPU             Measurement         `json:"cpu_busy_percent"`
	CPUInterval     time.Duration       `json:"cpu_interval_ns"`
	Cores           []MonitorCPU        `json:"cores"`
	Memory          ResourceMeasurement `json:"memory"`
	Swap            ResourceMeasurement `json:"swap"`
	Filesystem      ResourceMeasurement `json:"filesystem"`
	FilesystemStale bool                `json:"filesystem_stale"`
	DiskIO          []MonitorIO         `json:"disk_io"`
	Network         []MonitorIO         `json:"network"`
	DiskQuality     string              `json:"disk_quality"`
	NetworkQuality  string              `json:"network_quality"`
	DiskDetail      string              `json:"disk_detail,omitempty"`
	NetworkDetail   string              `json:"network_detail,omitempty"`
	GPU             Measurement         `json:"gpu_busy_percent"`
}
type MonitorHistoryIO struct {
	ID    string   `json:"id"`
	Read  *float64 `json:"read"`
	Write *float64 `json:"write"`
}
type MonitorPoint struct {
	Sequence            int64              `json:"sequence"`
	At                  time.Time          `json:"at"`
	CPU                 *float64           `json:"cpu_busy_percent"`
	MemoryUsed          *float64           `json:"memory_used_bytes"`
	MemoryAvailable     *float64           `json:"memory_available_bytes"`
	SwapUsed            *float64           `json:"swap_used_bytes"`
	FilesystemAvailable *float64           `json:"filesystem_available_bytes"`
	FilesystemAt        time.Time          `json:"filesystem_at"`
	DiskIO              []MonitorHistoryIO `json:"disk_io"`
	Network             []MonitorHistoryIO `json:"network"`
}
type MonitorQuery struct {
	Host  string `json:"host,omitempty"`
	Epoch string `json:"epoch,omitempty"`
	After int64  `json:"after"`
}
type MonitorPage struct {
	Host              string           `json:"host"`
	Epoch             string           `json:"epoch"`
	Cursor            int64            `json:"cursor"`
	Oldest            int64            `json:"oldest"`
	Gap               bool             `json:"gap"`
	Cadence           time.Duration    `json:"cadence_ns"`
	StaleAfter        time.Duration    `json:"stale_after_ns"`
	FilesystemCadence time.Duration    `json:"filesystem_cadence_ns"`
	Stale             bool             `json:"stale"`
	Age               time.Duration    `json:"age_ns"`
	Latest            *MonitorSnapshot `json:"latest"`
	History           []MonitorPoint   `json:"history"`
}
