// Package process implements safe, validated process termination. It
// deliberately never shells out - it only ever sends a signal to a PID
// that has been resolved through gopsutil, so there is no way for a
// caller to inject arbitrary shell commands.
package process

import (
	"errors"
	"fmt"
	"os"

	psProcess "github.com/shirou/gopsutil/v3/process"
)

var (
	ErrInvalidPID     = errors.New("invalid pid")
	ErrProtectedPID   = errors.New("refusing to terminate a protected process")
	ErrProcessMissing = errors.New("process not found")
)

// TerminateResult describes the outcome of a termination attempt for the
// API layer to serialize back to the client.
type TerminateResult struct {
	PID     int32  `json:"pid"`
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// Terminate validates pid and, if it is safe to do so, sends a graceful
// termination signal (SIGTERM equivalent via gopsutil) to it. It refuses
// to touch PID 1 (init) or the monitoring server's own PID so the tool
// can never be used to kill itself or the kernel's init process.
func Terminate(pid int32) TerminateResult {
	selfPID := int32(os.Getpid())

	if pid <= 0 {
		return TerminateResult{PID: pid, Success: false, Message: ErrInvalidPID.Error()}
	}
	if pid == 1 {
		return TerminateResult{PID: pid, Success: false, Message: "cannot terminate PID 1 (init)"}
	}
	if pid == selfPID {
		return TerminateResult{PID: pid, Success: false, Message: "cannot terminate the monitoring server itself"}
	}

	proc, err := psProcess.NewProcess(pid)
	if err != nil {
		return TerminateResult{PID: pid, Success: false, Message: ErrProcessMissing.Error()}
	}

	// Confirm the process still exists right before we act on it - it may
	// have exited between the request being made and being handled.
	exists, err := proc.IsRunning()
	if err != nil || !exists {
		return TerminateResult{PID: pid, Success: false, Message: ErrProcessMissing.Error()}
	}

	if err := proc.Terminate(); err != nil {
		// Fall back to a hard kill only if the graceful terminate failed
		// for a reason other than a permission error, which we surface
		// directly since the operator needs to know to elevate privileges.
		if isPermissionError(err) {
			return TerminateResult{
				PID:     pid,
				Success: false,
				Message: fmt.Sprintf("permission denied: %v", err),
			}
		}
		if killErr := proc.Kill(); killErr != nil {
			return TerminateResult{
				PID:     pid,
				Success: false,
				Message: fmt.Sprintf("failed to terminate process: %v", killErr),
			}
		}
	}

	return TerminateResult{PID: pid, Success: true, Message: "process terminated"}
}

func isPermissionError(err error) bool {
	return errors.Is(err, os.ErrPermission)
}
