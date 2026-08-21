// Package api wires up the REST endpoints and the /ws upgrade route on
// top of a gin.Engine. Handlers are intentionally thin - they validate
// input and delegate to collector/process/websocket, never touching the
// OS directly themselves.
package api

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"system-monitor/internal/collector"
	"system-monitor/internal/process"
	"system-monitor/internal/websocket"
)

// Server bundles the dependencies REST/WS handlers need.
type Server struct {
	Collector *collector.Collector
	Hub       *websocket.Hub
	StartedAt time.Time
}

// NewServer builds a Server ready to have its routes registered.
func NewServer(c *collector.Collector, hub *websocket.Hub) *Server {
	return &Server{
		Collector: c,
		Hub:       hub,
		StartedAt: time.Now(),
	}
}

// RegisterRoutes attaches every REST and WS endpoint to r.
func (s *Server) RegisterRoutes(r *gin.Engine) {
	r.GET("/ws", s.handleWS)

	apiGroup := r.Group("/api")
	{
		apiGroup.GET("/system", s.handleSystem)
		apiGroup.GET("/processes", s.handleProcesses)
		apiGroup.POST("/process/terminate", s.handleTerminate)
		apiGroup.GET("/health", s.handleHealth)
		apiGroup.GET("/info", s.handleInfo)
	}
}

func (s *Server) handleWS(c *gin.Context) {
	if err := s.Hub.ServeWS(c.Writer, c.Request); err != nil {
		// Upgrade failures happen for mundane reasons (e.g. a plain HTTP
		// GET to /ws from a browser address bar) - log lightly, don't 500.
		log.Printf("ws upgrade failed for %s: %v", c.ClientIP(), err)
		c.Status(http.StatusBadRequest)
	}
}

// handleSystem returns the latest full metrics snapshot - the same shape
// broadcast over the websocket - for clients that just want a one-shot
// poll instead of a live connection.
func (s *Server) handleSystem(c *gin.Context) {
	latest := s.Collector.Latest()
	if latest.CollectedAt.IsZero() {
		// Collector hasn't produced its first sample yet (e.g. hit right
		// after startup). Distinguish "no data yet" from "empty data".
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"message": "metrics not yet available, try again shortly",
		})
		return
	}
	c.JSON(http.StatusOK, latest)
}

func (s *Server) handleProcesses(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"processes": s.Collector.Latest().Processes,
	})
}

type terminateRequest struct {
	PID int32 `json:"pid" binding:"required"`
}

// Termination of PID 0 or 1 (or negative "process group" PIDs on POSIX)
// is never something this endpoint should allow - those are footguns,
// not legitimate targets for a "kill one process" dashboard action.
func isTerminatable(pid int32) bool {
	return pid > 1
}

func (s *Server) handleTerminate(c *gin.Context) {
	var req terminateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "invalid request body: pid (integer) is required",
		})
		return
	}

	if !isTerminatable(req.PID) {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "refusing to terminate reserved or invalid pid",
		})
		return
	}

	result := process.Terminate(req.PID)
	status := http.StatusOK
	if !result.Success {
		status = http.StatusUnprocessableEntity
	}
	c.JSON(status, result)
}

func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":         "ok",
		"uptimeSeconds":  int(time.Since(s.StartedAt).Seconds()),
		"connectedPeers": s.Hub.ClientCount(),
	})
}

func (s *Server) handleInfo(c *gin.Context) {
	latest := s.Collector.Latest()
	c.JSON(http.StatusOK, gin.H{
		"system": latest.System,
		"server": gin.H{"startedAt": s.StartedAt},
	})
}
