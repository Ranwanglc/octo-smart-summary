package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// LLMClient handles calls to the OpenAI-compatible LLM gateway.
type LLMClient struct {
	apiURL    string
	apiKey    string
	model     string
	timeout   time.Duration
	maxTokens int
	client    *http.Client
}

// NewLLMClient creates a new LLM client.
func NewLLMClient(apiURL, apiKey, model string, timeoutSec, maxTokens int) *LLMClient {
	return &LLMClient{
		apiURL:    strings.TrimRight(apiURL, "/"),
		apiKey:    apiKey,
		model:     model,
		timeout:   time.Duration(timeoutSec) * time.Second,
		maxTokens: maxTokens,
		client:    &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
}

// Call makes a chat completion request. Returns (content, tokenUsed, error).
func (c *LLMClient) Call(ctx context.Context, messages []chatMessage, temperature float64) (string, int, error) {
	reqBody := chatRequest{
		Model:       c.model,
		Messages:    messages,
		Temperature: temperature,
		MaxTokens:   c.maxTokens,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("LLM API error: status=%d body=%s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", 0, fmt.Errorf("unmarshal LLM response: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return "", 0, fmt.Errorf("LLM returned no choices")
	}
	return chatResp.Choices[0].Message.Content, chatResp.Usage.TotalTokens, nil
}

// CallRaw is a simple single-turn call returning text only. Used for topic narrowing.
func (c *LLMClient) CallRaw(ctx context.Context, prompt string) (string, error) {
	content, _, err := c.Call(ctx, []chatMessage{{Role: "user", Content: prompt}}, 0.3)
	if err != nil {
		log.Printf("[llm] CallRaw failed: %v", err)
		return "[]", nil
	}
	return content, nil
}

// ModelVersion returns the configured model name.
func (c *LLMClient) ModelVersion() string {
	return c.model
}

const mapSystemPrompt = `你是一个专业的工作内容整理助手。请总结以下聊天记录的关键内容。

输出格式：
### 讨论主题
（列出主要话题，每条一行）

### 关键决策与结论
（达成的共识、确定的方案）

### 待办事项
（以 - [ ] 格式列出，标注负责人）

要求：
- 只保留有实质内容的信息
- 忽略闲聊、表情、打招呼
- 如果没有明确结论，如实说明
- 保持简洁，不要重复原文`

const reduceSystemPrompt = `你是一个专业的工作内容整理助手。请将以下多个分片总结合并为一份完整的总结报告。

输出格式：
## 总结报告

**信息来源**：%s
**时间范围**：%s ~ %s
**消息总量**：%d 条

---

### 概要

### 主要议题与讨论

### 关键决策

### 待办事项

要求：
- 合并相同主题，去除重复
- 保留所有待办事项和责任人
- 如有冲突信息，保留最新的`

// CallMap runs the Map phase for a message chunk.
func (c *LLMClient) CallMap(ctx context.Context, formattedMessages string, sourceName string, chunkIndex int, msgCount int, timeStart, timeEnd string) (string, int, error) {
	if strings.TrimSpace(formattedMessages) == "" {
		return "(该时段无文本消息)", 0, nil
	}

	userPrompt := fmt.Sprintf("来源：%s\n时间范围：%s ~ %s\n消息数：%d 条\n\n聊天记录：\n%s",
		sourceName, timeStart, timeEnd, msgCount, formattedMessages)

	for attempt := 0; attempt < 3; attempt++ {
		content, tokens, err := c.Call(ctx, []chatMessage{
			{Role: "system", Content: mapSystemPrompt},
			{Role: "user", Content: userPrompt},
		}, 0.3)
		if err == nil {
			return content, tokens, nil
		}
		log.Printf("[llm] Map chunk %d attempt %d failed: %v", chunkIndex, attempt+1, err)
		if attempt < 2 {
			time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
		}
	}
	return fmt.Sprintf("(分片 %d 总结失败)", chunkIndex), 0, nil
}

// CallReduce runs the Reduce phase to merge chunk summaries.
func (c *LLMClient) CallReduce(ctx context.Context, chunkSummaries []string, sourceNames string, startTime, endTime string, totalMsgCount int) (string, int, error) {
	if len(chunkSummaries) == 1 {
		return chunkSummaries[0], 0, nil
	}

	var parts []string
	for i, s := range chunkSummaries {
		parts = append(parts, fmt.Sprintf("【分片 %d】\n%s", i+1, s))
	}
	summariesText := strings.Join(parts, "\n\n---\n\n")
	system := fmt.Sprintf(reduceSystemPrompt, sourceNames, startTime, endTime, totalMsgCount)

	return c.Call(ctx, []chatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: "以下是各分片的总结，请合并：\n\n" + summariesText},
	}, 0.3)
}

// CallReduceByPerson merges participant-level summaries.
func (c *LLMClient) CallReduceByPerson(ctx context.Context, participantSummaries []struct{ Name, Summary string }, startTime, endTime string) (string, int, error) {
	var parts []string
	for _, ps := range participantSummaries {
		parts = append(parts, fmt.Sprintf("【%s 的工作总结】\n%s", ps.Name, ps.Summary))
	}
	text := strings.Join(parts, "\n\n---\n\n")

	return c.Call(ctx, []chatMessage{
		{Role: "system", Content: "你是专业的工作汇报整理助手，请将多位成员的工作总结合并为团队整体总结报告。"},
		{Role: "user", Content: fmt.Sprintf("时间范围：%s ~ %s\n\n%s", startTime, endTime, text)},
	}, 0.3)
}
