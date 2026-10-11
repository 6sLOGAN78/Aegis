package control

import (
	"testing"
	"time"
)

func TestGatewayStaleAfterEnv(t *testing.T) {
	cases := map[string]time.Duration{
		"":     15 * time.Minute,
		"2m":   2 * time.Minute,
		"1m":   15 * time.Minute,
		"30s":  15 * time.Minute,
		"junk": 15 * time.Minute,
		"0":    15 * time.Minute,
		"-5m":  15 * time.Minute,
	}
	for in, want := range cases {
		t.Setenv("AEGIS_GATEWAY_STALE_AFTER", in)
		if got := gatewayStaleAfter(); got != want {
			t.Errorf("env %q: got %v want %v", in, got, want)
		}
	}
}

func TestIsStaleReplicaBoundaries(t *testing.T) {
	now := time.Now().UTC()
	def := 15 * time.Minute
	tests := []struct {
		name   string
		age    time.Duration
		status string
		after  time.Duration
		stale  bool
	}{
		{"30s present", 30 * time.Second, "ACK_STATUS_APPLIED", def, false},
		{"90s partitioned but present", 90 * time.Second, "ACK_STATUS_APPLIED", def, false},
		{"5m present", 5 * time.Minute, "ACK_STATUS_APPLIED", def, false},
		{"20m absent", 20 * time.Minute, "ACK_STATUS_APPLIED", def, true},
		{"2m threshold 5m absent", 5 * time.Minute, "ACK_STATUS_APPLIED", 2 * time.Minute, true},
		{"2m threshold 90s present", 90 * time.Second, "ACK_STATUS_APPLIED", 2 * time.Minute, false},
		{"ACK_STATUS_REJECTED 20m still listed", 20 * time.Minute, "ACK_STATUS_REJECTED", def, false},
		{"ACK_STATUS_REJECTED 23h still listed", 23 * time.Hour, "ACK_STATUS_REJECTED", def, false},
		{"ACK_STATUS_REJECTED 25h aged out", 25 * time.Hour, "ACK_STATUS_REJECTED", def, true},
		{"ACK_STATUS_REJECTED 4x long threshold", 30 * time.Hour, "ACK_STATUS_REJECTED", 8 * time.Hour, false},
		{"ACK_STATUS_REJECTED beyond 4x long threshold", 33 * time.Hour, "ACK_STATUS_REJECTED", 8 * time.Hour, true},
	}
	for _, tc := range tests {
		if got := isStaleReplica(now, now.Add(-tc.age), tc.status, tc.after); got != tc.stale {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.stale)
		}
	}
}
