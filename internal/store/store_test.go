package store

import (
	"testing"
	"time"
)

func TestAfterHistoryCursor(t *testing.T) {
	cursor := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		endedAt   time.Time
		sessionID string
		want      bool
	}{
		{name: "older", endedAt: cursor.Add(-time.Second), sessionID: "z", want: false},
		{name: "same lower session", endedAt: cursor, sessionID: "a", want: false},
		{name: "same higher session", endedAt: cursor, sessionID: "z", want: true},
		{name: "newer", endedAt: cursor.Add(time.Second), sessionID: "a", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := afterHistoryCursor(tt.endedAt, tt.sessionID, cursor, "m")
			if got != tt.want {
				t.Fatalf("afterHistoryCursor() = %v, want %v", got, tt.want)
			}
		})
	}
}
