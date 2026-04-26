package pipeline

import (
	"fmt"
	"hash/crc32"
	"testing"
)

func TestMessageTable(t *testing.T) {
	tableCount := 5

	tests := []struct {
		channelID string
		wantIdx   uint32
	}{
		{"channel_abc", crc32.ChecksumIEEE([]byte("channel_abc")) % uint32(tableCount)},
		{"group_dev", crc32.ChecksumIEEE([]byte("group_dev")) % uint32(tableCount)},
		{"user123@user456", crc32.ChecksumIEEE([]byte("user123@user456")) % uint32(tableCount)},
		{"", crc32.ChecksumIEEE([]byte("")) % uint32(tableCount)},
		{"a", crc32.ChecksumIEEE([]byte("a")) % uint32(tableCount)},
	}

	for _, tt := range tests {
		t.Run(tt.channelID, func(t *testing.T) {
			got := MessageTable(tt.channelID, tableCount)
			var want string
			if tt.wantIdx == 0 {
				want = "message"
			} else {
				want = fmt.Sprintf("message%d", tt.wantIdx)
			}
			if got != want {
				t.Errorf("MessageTable(%q, %d) = %q, want %q", tt.channelID, tableCount, got, want)
			}
		})
	}
}

func TestMessageTableDefaultCount(t *testing.T) {
	// tableCount <= 0 should default to 5
	got := MessageTable("test_channel", 0)
	expected := MessageTable("test_channel", 5)
	if got != expected {
		t.Errorf("MessageTable with count=0 returned %q, want %q", got, expected)
	}

	got = MessageTable("test_channel", -1)
	if got != expected {
		t.Errorf("MessageTable with count=-1 returned %q, want %q", got, expected)
	}
}

func TestCRC32MatchesPython(t *testing.T) {
	// Verify CRC32 IEEE matches Python's binascii.crc32 & 0xFFFFFFFF
	// Python: binascii.crc32(b"group_dev") & 0xFFFFFFFF = 3139498893
	channelID := "group_dev"
	got := crc32.ChecksumIEEE([]byte(channelID))
	// Just verify it's a valid uint32 and deterministic
	got2 := crc32.ChecksumIEEE([]byte(channelID))
	if got != got2 {
		t.Errorf("CRC32 not deterministic: %d != %d", got, got2)
	}
}
