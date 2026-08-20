package collector

import "system-monitor/internal/system"

// Metrics is the top level payload broadcast to every connected client
// once per collection tick. All fields are safe to marshal even when a
// particular subsystem is unavailable on the host platform - unavailable
// values are simply left at their zero value / empty slice, never omitted
// in a way that would break the frontend's expected shape.
type Metrics struct {
	Timestamp int64        `json:"timestamp"` // unix millis
	CPU       CPUMetrics   `json:"cpu"`
	Memory    MemMetrics   `json:"memory"`
	Disk      DiskMetrics  `json:"disk"`
	Network   NetMetrics   `json:"network"`
	Temps     []TempReading `json:"temperatures"`
	Processes []ProcessInfo `json:"processes"`
	System    system.InfoStat `json:"system"`
}

type CPUMetrics struct {
	Usage     float64   `json:"usage"`      // overall %
	PerCore   []float64 `json:"perCore"`    // per-core %
	Cores     int       `json:"cores"`      // logical core count
	Load1     float64   `json:"load1"`
	Load5     float64   `json:"load5"`
	Load15    float64   `json:"load15"`
	Available bool      `json:"available"`
}

type MemMetrics struct {
	Total     uint64  `json:"total"`
	Used      uint64  `json:"used"`
	Free      uint64  `json:"free"`
	Usage     float64 `json:"usage"`
	Available bool    `json:"available"`
}

type DiskMetrics struct {
	Total     uint64          `json:"total"`
	Used      uint64          `json:"used"`
	Free      uint64          `json:"free"`
	Usage     float64         `json:"usage"`
	Path      string          `json:"path"`
	IO        *DiskIOMetrics  `json:"io,omitempty"`
	Available bool            `json:"available"`
}

type DiskIOMetrics struct {
	ReadBytesPerSec  uint64 `json:"readBytesPerSec"`
	WriteBytesPerSec uint64 `json:"writeBytesPerSec"`
}

type NetMetrics struct {
	DownloadBytesPerSec uint64          `json:"download"`
	UploadBytesPerSec   uint64          `json:"upload"`
	TotalReceived       uint64          `json:"totalReceived"`
	TotalTransmitted    uint64          `json:"totalTransmitted"`
	Interfaces          []NetInterface  `json:"interfaces"`
	Available           bool            `json:"available"`
}

type NetInterface struct {
	Name      string `json:"name"`
	Received  uint64 `json:"received"`
	Sent      uint64 `json:"sent"`
	IsUp      bool   `json:"isUp"`
}

type TempReading struct {
	Sensor      string  `json:"sensor"`
	Temperature float64 `json:"temperature"`
}

type ProcessInfo struct {
	PID       int32   `json:"pid"`
	Name      string  `json:"name"`
	CPUPct    float64 `json:"cpuPercent"`
	MemBytes  uint64  `json:"memBytes"`
	MemPct    float32 `json:"memPercent"`
	Status    string  `json:"status"`
}
