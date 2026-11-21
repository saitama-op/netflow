package main

import (
	"context"
	"log"
	"sync"
	"time"
)

type FlowCache struct {
	mu           sync.RWMutex
	ConnToDevice map[string]string // "Proto:PublicIP:Port" -> Internal IP
	ConnToISP    map[string]string // "Proto:PublicIP:Port" -> ISP Name
	DeviceExpiry map[string]time.Time
	ISPExpiry    map[string]time.Time
}

var Cache = &FlowCache{
	ConnToDevice: make(map[string]string),
	ConnToISP:    make(map[string]string),
	DeviceExpiry: make(map[string]time.Time),
	ISPExpiry:    make(map[string]time.Time),
}

func StartCacheGC(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		now := time.Now()
		removed := 0

		Cache.mu.Lock()
		for key, expiresAt := range Cache.DeviceExpiry {
			if !now.Before(expiresAt) {
				delete(Cache.ConnToDevice, key)
				delete(Cache.DeviceExpiry, key)
				removed++
			}
		}
		for key, expiresAt := range Cache.ISPExpiry {
			if !now.Before(expiresAt) {
				delete(Cache.ConnToISP, key)
				delete(Cache.ISPExpiry, key)
				removed++
			}
		}
		Cache.mu.Unlock()

		if removed > 0 {
			log.Printf("Expired %d NAT correlation cache entries", removed)
		}
	}
}

func (c *FlowCache) setDevice(key, value string) {
	c.mu.Lock()
	c.ConnToDevice[key] = value
	c.DeviceExpiry[key] = time.Now().Add(10 * time.Minute)
	c.mu.Unlock()
}

func (c *FlowCache) setISP(key, value string) {
	c.mu.Lock()
	c.ConnToISP[key] = value
	c.ISPExpiry[key] = time.Now().Add(10 * time.Minute)
	c.mu.Unlock()
}

func (c *FlowCache) getDevice(key string) string {
	c.mu.RLock()
	value := c.ConnToDevice[key]
	expiresAt, found := c.DeviceExpiry[key]
	c.mu.RUnlock()
	if found && !time.Now().Before(expiresAt) {
		c.mu.Lock()
		if latestExpiry, exists := c.DeviceExpiry[key]; exists && time.Now().Before(latestExpiry) {
			value = c.ConnToDevice[key]
		} else {
			delete(c.ConnToDevice, key)
			delete(c.DeviceExpiry, key)
			value = ""
		}
		c.mu.Unlock()
		return value
	}
	return value
}

func (c *FlowCache) getISP(key string) string {
	c.mu.RLock()
	value := c.ConnToISP[key]
	expiresAt, found := c.ISPExpiry[key]
	c.mu.RUnlock()
	if found && !time.Now().Before(expiresAt) {
		c.mu.Lock()
		if latestExpiry, exists := c.ISPExpiry[key]; exists && time.Now().Before(latestExpiry) {
			value = c.ConnToISP[key]
		} else {
			delete(c.ConnToISP, key)
			delete(c.ISPExpiry, key)
			value = ""
		}
		c.mu.Unlock()
		return value
	}
	return value
}
