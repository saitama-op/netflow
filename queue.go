package main

import (
	"context"
	"log"
	"math"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type PendingFlow struct {
	Flow       Flow
	ConnKey    string
	InternalIP string
	ISPName    string
	Direction  string
	Group      string
	ExpiresAt  time.Time
}

var PendingQueue []PendingFlow
var QueueMu sync.Mutex

const MaxPendingQueueSize = 100000

func EnqueuePending(pending PendingFlow) bool {
	QueueMu.Lock()
	defer QueueMu.Unlock()
	if len(PendingQueue) >= MaxPendingQueueSize {
		log.Printf("dropping pending NetFlow due to queue limit: conn_key=%s", pending.ConnKey)
		return false
	}
	PendingQueue = append(PendingQueue, pending)
	return true
}

var NetflowBytes = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "netflow_bytes_total",
		Help: "NetFlow traffic in bytes",
	},
	[]string{"sampler", "proto", "interface", "group", "direction", "client_ip"},
)

var NetflowFlows = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "netflow_flows_total",
		Help: "NetFlow flow count",
	},
	[]string{"sampler", "proto", "interface", "group", "direction", "client_ip"},
)

func ExportMetric(flow Flow, direction, ispName, group, internalIP, connKey string) {
	if flow.Bytes <= 0 || math.IsNaN(flow.Bytes) || math.IsInf(flow.Bytes, 0) {
		log.Printf("dropping invalid flow byte count: bytes=%.0f conn_key=%s", flow.Bytes, connKey)
		return
	}

	NetflowBytes.WithLabelValues(
		flow.SamplerAddress, flow.Proto, ispName, group, direction, internalIP,
	).Add(flow.Bytes)

	NetflowFlows.WithLabelValues(
		flow.SamplerAddress, flow.Proto, ispName, group, direction, internalIP,
	).Inc()

	log.Printf(
		"dir=%s interface=%s group=%s internal_ip=%s proto=%s port_key=%s bytes=%.0f",
		direction, ispName, group, internalIP, flow.Proto, connKey, flow.Bytes,
	)
}

func StartQueueWorker(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			processPendingQueue(time.Now(), true)
			return
		case now := <-ticker.C:
			processPendingQueue(now, false)
		}
	}
}

func processPendingQueue(now time.Time, flush bool) {
	QueueMu.Lock()
	defer QueueMu.Unlock()

	remaining := PendingQueue[:0]
	for _, pf := range PendingQueue {
		resolved := false

		if pf.Direction == "upload" {
			isp := Cache.getISP(pf.ConnKey)
			if isp != "" && isp != "UNKNOWN_ISP" {
				ExportMetric(pf.Flow, pf.Direction, isp, pf.Group, pf.InternalIP, pf.ConnKey)
				resolved = true
			}
		} else if pf.Direction == "download" {
			device := Cache.getDevice(pf.ConnKey)
			if device != "" && device != "UNKNOWN_DEVICE" {
				ExportMetric(pf.Flow, pf.Direction, pf.ISPName, DeviceGroup(device), device, pf.ConnKey)
				resolved = true
			}
		}

		if resolved {
			continue
		}
		if flush || !now.Before(pf.ExpiresAt) {
			if pf.Direction == "upload" {
				ExportMetric(pf.Flow, pf.Direction, "UNKNOWN_ISP", pf.Group, pf.InternalIP, pf.ConnKey)
			} else {
				ExportMetric(pf.Flow, pf.Direction, pf.ISPName, DeviceGroup(pf.InternalIP), pf.InternalIP, pf.ConnKey)
			}
			continue
		}
		remaining = append(remaining, pf)
	}

	PendingQueue = remaining
}
