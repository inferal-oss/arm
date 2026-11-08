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
	Pressure        MonitorPressure     `json:"pressure"`
}

// Pressure levels order resource contention. Unknown means the signals were
// missing, stale or unsupported; it never justifies remedial action.
const (
	PressureUnknown  = "unknown"
	PressureNormal   = "normal"
	PressureElevated = "elevated"
	PressureHigh     = "high"
	PressureCritical = "critical"
)

// PressureRank orders levels; unknown ranks below normal and reports false.
func PressureRank(level string) (int, bool) {
	switch level {
	case PressureNormal:
		return 0, true
	case PressureElevated:
		return 1, true
	case PressureHigh:
		return 2, true
	case PressureCritical:
		return 3, true
	}
	return -1, false
}

type PressureSignal struct {
	ID    string      `json:"id"`
	Label string      `json:"label"`
	Value Measurement `json:"value"`
	// Level is the level this signal alone indicates.
	Level string `json:"level"`
}
type PressureReading struct {
	Level   string           `json:"level"`
	Reason  string           `json:"reason,omitempty"`
	Signals []PressureSignal `json:"signals"`
}
type MonitorPressure struct {
	CPU    PressureReading `json:"cpu"`
	Memory PressureReading `json:"memory"`
	IO     PressureReading `json:"io"`
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
	CPUPressure         *int               `json:"cpu_pressure_rank"`
	MemoryPressure      *int               `json:"memory_pressure_rank"`
	IOPressure          *int               `json:"io_pressure_rank"`
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
