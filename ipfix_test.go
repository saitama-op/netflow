package main

import (
	"encoding/binary"
	"testing"
)

func TestSplitIPFIXMessageAndRebuild(t *testing.T) {
	header := make([]byte, 16)
	binary.BigEndian.PutUint16(header[0:2], 10)
	binary.BigEndian.PutUint32(header[12:16], 42)
	sets := []ipfixSet{
		{id: 259, data: []byte{1, 3, 0, 4}},
		{id: 2, data: []byte{0, 2, 0, 4}},
		{id: 258, data: []byte{1, 2, 0, 4}},
	}
	payload := buildIPFIXMessage(header, sets)

	gotHeader, gotSets, domain, err := splitIPFIXMessage(payload)
	if err != nil {
		t.Fatalf("splitIPFIXMessage() error = %v", err)
	}
	if domain != 42 {
		t.Fatalf("observation domain = %d, want 42", domain)
	}
	if len(gotSets) != len(sets) {
		t.Fatalf("set count = %d, want %d", len(gotSets), len(sets))
	}

	filtered, missing := selectIPFIXDataSets(gotSets, func(id uint16) bool {
		return id == 258
	})
	if len(filtered) != 1 || filtered[0].id != 258 {
		t.Fatalf("decodable sets = %#v, want only template 258 data", filtered)
	}
	if len(missing) != 1 || missing[0] != 259 {
		t.Fatalf("missing templates = %v, want [259]", missing)
	}

	rebuilt := buildIPFIXMessage(gotHeader, filtered)
	if got, want := int(binary.BigEndian.Uint16(rebuilt[2:4])), len(rebuilt); got != want {
		t.Fatalf("rebuilt message length field = %d, want %d", got, want)
	}
	if _, rebuiltSets, _, err := splitIPFIXMessage(rebuilt); err != nil || len(rebuiltSets) != 1 || rebuiltSets[0].id != 258 {
		t.Fatalf("rebuilt packet is invalid or has unexpected sets: sets=%v error=%v", rebuiltSets, err)
	}
}

func TestSplitIPFIXMessageRejectsTruncatedSet(t *testing.T) {
	header := make([]byte, 16)
	binary.BigEndian.PutUint16(header[0:2], 10)
	payload := append(header, 0, 2, 0, 8)
	binary.BigEndian.PutUint16(payload[2:4], uint16(len(payload)))

	if _, _, _, err := splitIPFIXMessage(payload); err == nil {
		t.Fatal("expected truncated set to be rejected")
	}
}
