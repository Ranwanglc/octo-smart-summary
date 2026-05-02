package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode/utf8"
)

// TimeNarrowResult LLM 解析 topic 时间范围的输出
type TimeNarrowResult struct {
	HasTimeExpr bool   `json:"has_time_expr"`
	Start       string `json:"start"`
	End         string `json:"end"`
	Reasoning   string `json:"reasoning"`
}

func trimMarkdownCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if idx := strings.Index(s, "\n"); idx >= 0 {
			s = s[idx+1:]
		}
	}
	if idx := strings.LastIndex(s, "```"); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

func sanitizeTopic(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r < 32 {
			return ' '
		}
		return r
	}, s)
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return strings.TrimSpace(s)
}

// PreRetrievalNarrow 用 LLM 从 topic 中提取时间范围，缩窄查询窗口。
func PreRetrievalNarrow(ctx context.Context, topic string, originalStart, originalEnd time.Time, llmFn LLMCallFn) (time.Time, time.Time) {
	if topic == "" || llmFn == nil {
		return originalStart, originalEnd
	}
	if utf8.RuneCountInString(topic) > 500 {
		runes := []rune(topic)
		topic = string(runes[:500])
	}
	topic = sanitizeTopic(topic)

	now := time.Now()
	currentDate := now.Format("2006-01-02")
	weekdays := [...]string{"日", "一", "二", "三", "四", "五", "六"}
	weekday := weekdays[now.Weekday()]

	prompt := fmt.Sprintf(`你是一个时间表达式解析器。

当前日期：%s（星期%s）
当前时间：%s

用户输入的主题："%s"

任务：判断主题中是否包含时间表达式（如"今天"、"昨天"、"本周"、"上周"、"最近三天"等），如果有，解析出精确的时间范围。

返回 JSON（只返回 JSON，不要其他内容）：
{
  "has_time_expr": true,
  "start": "2026-05-01T00:00:00+08:00",
  "end": "2026-05-01T23:59:59+08:00",
  "reasoning": "一句话解释"
}

规则：
- 如果没有任何时间表达式，返回 {"has_time_expr": false, "start": "", "end": "", "reasoning": "无时间表达式"}
- "今天" = 当天 00:00:00 ~ 23:59:59
- "昨天" = 前一天 00:00:00 ~ 23:59:59
- "本周" = 本周一 00:00:00 ~ 当前时间
- "上周" = 上周一 00:00:00 ~ 上周日 23:59:59
- "最近N天" = N天前 00:00:00 ~ 当前时间
- "这几天" = 最近3天
- 时区统一使用 +08:00`, currentDate, weekday, now.Format("15:04"), topic)

	narrowStart := time.Now()
	result, err := llmFn(ctx, prompt)
	elapsed := time.Since(narrowStart).Milliseconds()

	if err != nil {
		log.Printf("[pipeline-personal] PreRetrievalNarrow failed (%dms): %v, using original range", elapsed, err)
		return originalStart, originalEnd
	}

	cleaned := trimMarkdownCodeFence(result)

	var parsed TimeNarrowResult
	if err := json.Unmarshal([]byte(cleaned), &parsed); err != nil {
		log.Printf("[pipeline-personal] PreRetrievalNarrow parse error (%dms): %v, response=%s", elapsed, err, result)
		return originalStart, originalEnd
	}

	if !parsed.HasTimeExpr {
		log.Printf("[pipeline-personal] PreRetrievalNarrow (%dms): no time expr in topic", elapsed)
		return originalStart, originalEnd
	}

	parsedStart, err1 := time.Parse(time.RFC3339, parsed.Start)
	parsedEnd, err2 := time.Parse(time.RFC3339, parsed.End)
	if err1 != nil || err2 != nil {
		log.Printf("[pipeline-personal] PreRetrievalNarrow (%dms): time parse error start=%v end=%v", elapsed, err1, err2)
		return originalStart, originalEnd
	}

	if parsedStart.Before(originalStart) {
		parsedStart = originalStart
	}
	if parsedEnd.After(originalEnd) {
		parsedEnd = originalEnd
	}
	if !parsedStart.Before(parsedEnd) {
		log.Printf("[pipeline-personal] PreRetrievalNarrow (%dms): invalid range start >= end", elapsed)
		return originalStart, originalEnd
	}

	log.Printf("[pipeline-personal] PreRetrievalNarrow (%dms): narrowed [%s ~ %s] → [%s ~ %s] reason=%s",
		elapsed,
		originalStart.Format("01-02"), originalEnd.Format("01-02"),
		parsedStart.Format("01-02 15:04"), parsedEnd.Format("01-02 15:04"),
		parsed.Reasoning)

	return parsedStart, parsedEnd
}

// PostRetrievalNarrow 召回后的语义过滤（预留接口）。
func PostRetrievalNarrow(ctx context.Context, messages []Message, topic string, llmFn LLMCallFn) []Message {
	return messages
}
