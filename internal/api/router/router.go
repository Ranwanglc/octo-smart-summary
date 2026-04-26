package router

import (
	"net/http"

	"github.com/Mininglamp-OSS/octo-smart-summary/internal/api/handler"
	"github.com/Mininglamp-OSS/octo-smart-summary/internal/api/ws"
	"github.com/Mininglamp-OSS/octo-smart-summary/internal/middleware"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// SetupPublic configures the public API router on :8080.
func SetupPublic(db *gorm.DB, hub *ws.Hub) *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	// CORS
	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type,Authorization,Token,X-Space-Id,X-User-Id")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	})

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// WebSocket
	r.GET("/ws/summaries", hub.HandleWS)
	r.GET("/ws", hub.HandleWS)

	// API routes
	taskH := handler.NewTaskHandler(db)
	schedH := handler.NewScheduleHandler(db)

	v1 := r.Group("/api/v1")
	v1.Use(middleware.AuthMiddleware(), middleware.SpaceMiddleware())
	{
		v1.POST("/summaries", taskH.CreateSummary)
		v1.GET("/summaries", taskH.ListSummaries)
		v1.GET("/summaries/:id", taskH.GetSummary)
		v1.GET("/summaries/:id/result", taskH.GetResult)
		v1.POST("/summaries/:id/regenerate", taskH.Regenerate)
		v1.GET("/summary-infer", taskH.InferScope)

		v1.POST("/summary-schedules", schedH.CreateSchedule)
		v1.GET("/summary-schedules", schedH.ListSchedules)
		v1.GET("/summary-schedules/:id", schedH.GetSchedule)
		v1.PUT("/summary-schedules/:id", schedH.UpdateSchedule)
		v1.DELETE("/summary-schedules/:id", schedH.DeleteSchedule)
		v1.PUT("/summary-schedules/:id/toggle", schedH.ToggleSchedule)
	}

	return r
}

// SetupInternal configures the internal API router on :8081.
func SetupInternal(hub *ws.Hub) *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	intH := handler.NewInternalHandler(hub)
	r.POST("/internal/task-event", intH.TaskEvent)

	return r
}
