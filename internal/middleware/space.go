package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// SpaceMiddleware extracts X-Space-Id header and sets it in context.
// Returns 400 if the header is missing.
func SpaceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		spaceID := c.GetHeader("X-Space-Id")
		if spaceID == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"code":    4001,
				"message": "missing space_id",
			})
			return
		}
		c.Set("space_id", spaceID)
		c.Next()
	}
}

// GetSpaceID retrieves space_id from gin context.
func GetSpaceID(c *gin.Context) string {
	v, _ := c.Get("space_id")
	s, _ := v.(string)
	return s
}

// AuthMiddleware extracts Token header and resolves uid.
// For now it reads X-User-Id / Token headers.
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetHeader("X-User-Id")
		token := c.GetHeader("Token")
		c.Set("user_id", userID)
		c.Set("token", token)
		c.Next()
	}
}

// GetUserID retrieves user_id from gin context.
func GetUserID(c *gin.Context) string {
	v, _ := c.Get("user_id")
	s, _ := v.(string)
	return s
}
