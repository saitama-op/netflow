package main

import "testing"

func TestEnqueuePendingHonorsLimit(t *testing.T) {
	QueueMu.Lock()
	PendingQueue = make([]PendingFlow, MaxPendingQueueSize)
	QueueMu.Unlock()

	if EnqueuePending(PendingFlow{ConnKey: "overflow"}) {
		t.Fatal("expected queue overflow")
	}

	QueueMu.Lock()
	PendingQueue = nil
	QueueMu.Unlock()
}
