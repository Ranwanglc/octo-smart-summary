package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// ChannelInfo holds discovered channel metadata.
type ChannelInfo struct {
	ChannelID   string `json:"channel_id"`
	ChannelType int    `json:"channel_type"`
	ChannelName string `json:"channel_name"`
	SpaceID     string `json:"space_id,omitempty"`
}

// Message represents a fetched chat message.
type Message struct {
	MessageSeq int64  `json:"message_seq"`
	SenderUID  string `json:"sender_uid"`
	ChannelID  string `json:"channel_id"`
	Timestamp  int64  `json:"timestamp"`
	SendTime   string `json:"send_time"`
	Content    string `json:"content"`
	SourceName string `json:"source_name"`
}

// LLMCallFn is the type for the LLM topic-narrowing function.
type LLMCallFn func(ctx context.Context, prompt string) (string, error)

// GetUserChannels discovers all channels (group + DM) for a user. (Layer 1)
func GetUserChannels(ctx context.Context, uid string, imDB *gorm.DB) ([]ChannelInfo, error) {
	if imDB == nil {
		return nil, fmt.Errorf("IM database not available")
	}

	var channels []ChannelInfo

	// Groups
	type groupRow struct {
		ChannelID   string `gorm:"column:channel_id"`
		ChannelType int    `gorm:"column:channel_type"`
		ChannelName string `gorm:"column:channel_name"`
		SpaceID     string `gorm:"column:space_id"`
	}
	var groups []groupRow
	err := imDB.WithContext(ctx).Raw(`
		SELECT g.group_no AS channel_id,
		       2 AS channel_type,
		       g.name AS channel_name,
		       COALESCE(g.space_id, '') AS space_id
		FROM `+"`group`"+` g
		INNER JOIN group_member gm ON g.group_no = gm.group_no
		WHERE gm.uid = ?
		  AND gm.is_deleted = 0
		  AND g.status = 1
		ORDER BY g.updated_at DESC
	`, uid).Scan(&groups).Error
	if err != nil {
		return nil, fmt.Errorf("query groups: %w", err)
	}
	for _, g := range groups {
		channels = append(channels, ChannelInfo{
			ChannelID:   g.ChannelID,
			ChannelType: g.ChannelType,
			ChannelName: g.ChannelName,
			SpaceID:     g.SpaceID,
		})
	}

	// DM channels
	type dmRow struct {
		ChannelID string `gorm:"column:channel_id"`
	}
	var dms []dmRow
	err = imDB.WithContext(ctx).Raw(`
		SELECT channel_id
		FROM conversation_extra
		WHERE uid = ? AND channel_type = 1
		GROUP BY channel_id
		ORDER BY MAX(updated_at) DESC
		LIMIT 200
	`, uid).Scan(&dms).Error
	if err != nil {
		log.Printf("[pipeline] query DM channels error: %v", err)
	}
	for _, d := range dms {
		peerUID := getPeerUID(d.ChannelID, uid)
		channels = append(channels, ChannelInfo{
			ChannelID:   d.ChannelID,
			ChannelType: 1,
			ChannelName: fmt.Sprintf("私聊-%s", peerUID),
		})
	}

	return channels, nil
}

func getPeerUID(channelID, selfUID string) string {
	parts := strings.SplitN(channelID, "@", 2)
	if len(parts) != 2 {
		return channelID
	}
	if parts[0] == selfUID {
		return parts[1]
	}
	return parts[0]
}

// ApplySourceConstraints filters channels to only those specified. (Layer 2)
func ApplySourceConstraints(userChannels []ChannelInfo, specifiedSources []map[string]interface{}) []ChannelInfo {
	if len(specifiedSources) == 0 {
		return userChannels
	}
	allowed := make(map[string]bool, len(userChannels))
	for _, ch := range userChannels {
		allowed[ch.ChannelID] = true
	}
	specified := make(map[string]bool, len(specifiedSources))
	for _, s := range specifiedSources {
		if id, ok := s["source_id"].(string); ok {
			specified[id] = true
		}
	}
	var result []ChannelInfo
	for _, ch := range userChannels {
		if specified[ch.ChannelID] && allowed[ch.ChannelID] {
			result = append(result, ch)
		}
	}
	return result
}

// NarrowByTopic uses LLM to filter channels relevant to the topic. (Layer 3)
func NarrowByTopic(ctx context.Context, topic string, candidates []ChannelInfo, llmFn LLMCallFn) []ChannelInfo {
	if topic == "" || len(candidates) == 0 || llmFn == nil {
		return candidates
	}

	var lines []string
	for _, c := range candidates {
		lines = append(lines, fmt.Sprintf("- %s: %s", c.ChannelID, c.ChannelName))
	}
	prompt := fmt.Sprintf(
		"用户想总结的主题是:\"%s\"\n\n候选频道列表:\n%s\n\n请从上面列表中选出与主题最相关的频道,返回 JSON 数组(只包含 channel_id):\n[\"id1\", \"id2\", ...]\n只返回 JSON,不要其他内容。",
		topic, strings.Join(lines, "\n"),
	)

	result, err := llmFn(ctx, prompt)
	if err != nil {
		return candidates
	}

	var selectedIDs []string
	if err := json.Unmarshal([]byte(result), &selectedIDs); err != nil {
		return candidates
	}

	idSet := make(map[string]bool, len(selectedIDs))
	for _, id := range selectedIDs {
		idSet[id] = true
	}

	var filtered []ChannelInfo
	for _, c := range candidates {
		if idSet[c.ChannelID] {
			filtered = append(filtered, c)
		}
	}
	if len(filtered) == 0 {
		return candidates
	}
	return filtered
}

// FetchMessagesFromChannel fetches text messages from a sharded table. (Layer 4)
func FetchMessagesFromChannel(ctx context.Context, channelID string, channelType int, startTS, endTS int64, imDB *gorm.DB, tableCount int) ([]Message, error) {
	if imDB == nil {
		return nil, fmt.Errorf("IM database not available")
	}
	table := MessageTable(channelID, tableCount)

	type msgRow struct {
		MessageSeq int64  `gorm:"column:message_seq"`
		FromUID    string `gorm:"column:from_uid"`
		ChannelID  string `gorm:"column:channel_id"`
		Timestamp  int64  `gorm:"column:timestamp"`
		Payload    []byte `gorm:"column:payload"`
	}
	var rows []msgRow

	query := fmt.Sprintf(
		"SELECT message_seq, from_uid, channel_id, `timestamp`, payload FROM `%s` WHERE channel_id = ? AND channel_type = ? AND `timestamp` BETWEEN ? AND ? AND is_deleted = 0 ORDER BY message_seq ASC LIMIT 5000",
		table,
	)
	if err := imDB.WithContext(ctx).Raw(query, channelID, channelType, startTS, endTS).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("fetch messages from %s: %w", table, err)
	}

	var messages []Message
	for _, r := range rows {
		text, ok := ExtractText(r.Payload)
		if !ok {
			continue
		}
		messages = append(messages, Message{
			MessageSeq: r.MessageSeq,
			SenderUID:  r.FromUID,
			ChannelID:  r.ChannelID,
			Timestamp:  r.Timestamp,
			SendTime:   time.Unix(r.Timestamp, 0).Format(time.RFC3339),
			Content:    text,
		})
	}
	return messages, nil
}

// ResolveAndFetchMessages runs the full 4-layer pipeline.
func ResolveAndFetchMessages(ctx context.Context, uid string, specifiedSources []map[string]interface{}, topic string, timeStart, timeEnd time.Time, imDB *gorm.DB, llmFn LLMCallFn, tableCount int) ([]Message, error) {
	if timeEnd.Sub(timeStart) > 30*24*time.Hour {
		return nil, fmt.Errorf("时间范围不能超过 30 天")
	}

	startTS := timeStart.Unix()
	endTS := timeEnd.Unix()

	// Layer 1: channel discovery
	userChannels, err := GetUserChannels(ctx, uid, imDB)
	if err != nil {
		return nil, fmt.Errorf("channel discovery: %w", err)
	}

	// Layer 2: source constraints
	candidates := ApplySourceConstraints(userChannels, specifiedSources)

	// Layer 3: topic narrowing (only when no specified sources)
	if len(specifiedSources) == 0 && topic != "" {
		candidates = NarrowByTopic(ctx, topic, candidates, llmFn)
	}

	// Layer 4: message fetching
	var allMessages []Message
	for _, ch := range candidates {
		msgs, err := FetchMessagesFromChannel(ctx, ch.ChannelID, ch.ChannelType, startTS, endTS, imDB, tableCount)
		if err != nil {
			log.Printf("[pipeline] fetch from %s error: %v", ch.ChannelID, err)
			continue
		}
		for i := range msgs {
			msgs[i].SourceName = ch.ChannelName
		}
		allMessages = append(allMessages, msgs...)
	}

	sort.Slice(allMessages, func(i, j int) bool {
		return allMessages[i].Timestamp < allMessages[j].Timestamp
	})
	return allMessages, nil
}
