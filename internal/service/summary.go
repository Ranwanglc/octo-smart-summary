package service

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/Mininglamp-OSS/octo-smart-summary/internal/model"
	"gorm.io/gorm"
)

// BizError represents a business logic error.
type BizError struct {
	Code       int    `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"-"`
}

func (e *BizError) Error() string {
	return fmt.Sprintf("[%d] %s", e.Code, e.Message)
}

// NewBizError creates a new BizError.
func NewBizError(code int, message string, httpStatus int) *BizError {
	return &BizError{Code: code, Message: message, HTTPStatus: httpStatus}
}

// GenerateTaskNo creates a unique task number.
func GenerateTaskNo() string {
	now := time.Now().UTC()
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return fmt.Sprintf("ST%s%s", now.Format("20060102"), string(b))
}

// ResolveSourceName returns a fallback source name.
func ResolveSourceName(sourceID string) string {
	if len(sourceID) > 8 {
		return "来源-" + sourceID[:8]
	}
	return "来源-" + sourceID
}

// GetNextVersion returns the next version number for a task result.
func GetNextVersion(db *gorm.DB, taskID int64) (int, error) {
	var maxVer *int
	err := db.Model(&model.SummaryResult{}).
		Where("task_id = ?", taskID).
		Select("MAX(version)").
		Scan(&maxVer).Error
	if err != nil {
		return 0, err
	}
	if maxVer == nil {
		return 1, nil
	}
	return *maxVer + 1, nil
}

// SplitIntoChunks splits messages into chunks of roughly chunkSize.
func SplitIntoChunks(messages []map[string]interface{}, chunkSize int) [][]map[string]interface{} {
	if chunkSize <= 0 {
		chunkSize = 500
	}
	if len(messages) <= chunkSize {
		return [][]map[string]interface{}{messages}
	}
	var chunks [][]map[string]interface{}
	for i := 0; i < len(messages); i += chunkSize {
		end := i + chunkSize
		if end > len(messages) {
			end = len(messages)
		}
		chunks = append(chunks, messages[i:end])
	}
	return chunks
}

// InferScope infers sources and summary_mode from a topic (keyword heuristic fallback).
func InferScope(topic string) map[string]interface{} {
	type source struct {
		SourceType int    `json:"source_type"`
		SourceID   string `json:"source_id"`
		SourceName string `json:"source_name"`
	}

	topicLower := topic
	var sources []source

	switch {
	case containsAny(topicLower, []string{"项目", "project", "进展", "进度"}):
		sources = []source{
			{1, "group_product", "产品团队群"},
			{1, "group_dev", "开发团队群"},
		}
	case containsAny(topicLower, []string{"会议", "meeting", "周会"}):
		sources = []source{
			{1, "group_all", "全员群"},
		}
	case containsAny(topicLower, []string{"技术", "tech", "架构", "代码"}):
		sources = []source{
			{1, "group_dev", "开发团队群"},
			{1, "group_infra", "基础架构群"},
		}
	default:
		sources = []source{
			{1, "group_general", "综合讨论群"},
		}
	}

	return map[string]interface{}{
		"sources":      sources,
		"summary_mode": 1,
		"inferred":     true,
	}
}

func containsAny(s string, keywords []string) bool {
	for _, kw := range keywords {
		if len(s) >= len(kw) {
			for i := 0; i <= len(s)-len(kw); i++ {
				if s[i:i+len(kw)] == kw {
					return true
				}
			}
		}
	}
	return false
}
