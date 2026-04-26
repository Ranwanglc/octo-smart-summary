package handler

import (
	"net/http"

	"github.com/Mininglamp-OSS/octo-smart-summary/internal/api/ws"
	"github.com/Mininglamp-OSS/octo-smart-summary/internal/model"
	"github.com/gin-gonic/gin"
)

// InternalHandler handles internal endpoints (Worker → API callbacks).
type InternalHandler struct {
	hub *ws.Hub
}

// NewInternalHandler creates a new InternalHandler.
func NewInternalHandler(hub *ws.Hub) *InternalHandler {
	return &InternalHandler{hub: hub}
}

// TaskEvent handles POST /internal/task-event
func (h *InternalHandler) TaskEvent(c *gin.Context) {
	var event model.TaskEvent
	if err := c.ShouldBindJSON(&event); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Broadcast to WebSocket subscribers
	h.hub.Broadcast(event.TaskID, gin.H{
		"type": "TASK_STATUS_CHANGED",
		"payload": gin.H{
			"task_id":  event.TaskID,
			"status":   event.Status,
			"progress": event.Progress,
			"message":  event.Message,
		},
	})

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
}
