package notify

import (
	"testing"
	"time"
)

func TestCacheTTLUsesDurationUnits(t *testing.T) {
	if cacheTTL != 5*time.Minute {
		t.Fatalf("cacheTTL = %s, want 5m", cacheTTL)
	}
}
