// Package system provides host-level identity information (hostname,
// kernel, architecture, uptime) that changes rarely, as opposed to the
// high-frequency metrics handled by the collector package.
package system

import (
	"runtime"

	"github.com/shirou/gopsutil/v3/host"
)

// InfoStat is the static host information reported alongside each
// metrics snapshot. It lives in this package (rather than collector) so
// collector can depend on system without creating an import cycle.
type InfoStat struct {
	OS           string `json:"os"`
	Platform     string `json:"platform"`
	KernelVer    string `json:"kernelVersion"`
	Hostname     string `json:"hostname"`
	Architecture string `json:"architecture"`
	UptimeSecs   uint64 `json:"uptimeSeconds"`
	BootTime     uint64 `json:"bootTime"`
}

// Info gathers static/slow-changing host information. It never returns an
// error to the caller - any failure to reach the OS just yields sensible
// zero values so the dashboard keeps rendering.
func Info() InfoStat {
	info := InfoStat{
		Architecture: runtime.GOARCH,
	}

	hi, err := host.Info()
	if err != nil || hi == nil {
		// Best-effort fallback so the header still shows something useful.
		info.OS = runtime.GOOS
		return info
	}

	info.OS = hi.OS
	info.Platform = hi.Platform
	info.KernelVer = hi.KernelVersion
	info.Hostname = hi.Hostname
	info.UptimeSecs = hi.Uptime
	info.BootTime = hi.BootTime

	return info
}
