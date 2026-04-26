package pipeline

import (
	"encoding/base64"
	"encoding/json"
)

type payloadData struct {
	Type    int    `json:"type"`
	Content string `json:"content"`
}

// ExtractText decodes a message payload (raw JSON or base64-encoded JSON).
// Returns the text content if type == 1, otherwise returns ("", false).
func ExtractText(payload []byte) (string, bool) {
	if len(payload) == 0 {
		return "", false
	}

	// Try raw JSON first
	var data payloadData
	if err := json.Unmarshal(payload, &data); err == nil {
		if data.Type == 1 && data.Content != "" {
			return data.Content, true
		}
		return "", false
	}

	// Try base64 decode then JSON
	decoded, err := base64.StdEncoding.DecodeString(string(payload))
	if err != nil {
		// Try URL-safe base64
		decoded, err = base64.URLEncoding.DecodeString(string(payload))
		if err != nil {
			// Try raw base64 (no padding)
			decoded, err = base64.RawStdEncoding.DecodeString(string(payload))
			if err != nil {
				return "", false
			}
		}
	}

	if err := json.Unmarshal(decoded, &data); err != nil {
		return "", false
	}
	if data.Type == 1 && data.Content != "" {
		return data.Content, true
	}
	return "", false
}
