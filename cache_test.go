package main

import (
	"testing"
	"time"
)

func TestFlowCacheExpiration(t *testing.T) {
	cache := &FlowCache{
		ConnToDevice: make(map[string]string),
		ConnToISP:    make(map[string]string),
		DeviceExpiry: make(map[string]time.Time),
		ISPExpiry:    make(map[string]time.Time),
	}

	cache.setDevice("tcp:1.1.1.1:443", "192.168.1.10")
	if got := cache.getDevice("tcp:1.1.1.1:443"); got != "192.168.1.10" {
		t.Fatalf("got %q", got)
	}

	cache.mu.Lock()
	cache.DeviceExpiry["tcp:1.1.1.1:443"] = time.Now().Add(-time.Second)
	cache.mu.Unlock()
	if got := cache.getDevice("tcp:1.1.1.1:443"); got != "" {
		t.Fatalf("expired cache entry returned %q", got)
	}
}
