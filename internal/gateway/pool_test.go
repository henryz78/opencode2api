package gateway

import (
	"testing"
	"time"

	"opencode2api/internal/config"
)

func TestAnonymousCursorFallsBackToEarliestCooldown(t *testing.T) {
	transports, err := transportPool([]string{"direct"}, config.PerformanceConfig{
		MaxIdleConns: 10, MaxIdleConnsPerHost: 2, ConnectTimeoutSeconds: 5,
	}, time.Second)
	if err != nil {
		t.Fatalf("newTransportPool error: %v", err)
	}
	pool := anonymousPool(true, transports, 15*time.Second)
	if pool.Len() != 1 {
		t.Fatalf("pool.Len() = %d, want 1", pool.Len())
	}
	node := pool.nodes[0]
	node.cooldownUntil.Store(time.Now().Add(10 * time.Second).UnixNano())

	cursor := pool.CursorFor("test-session")
	if got := cursor.Next(); got != node {
		t.Fatalf("cursor.Next() = %v, want cooling proxy fallback %v", got, node)
	}
	if second := cursor.Next(); second != nil {
		t.Fatalf("second cursor.Next() = %v, want nil", second)
	}
}
