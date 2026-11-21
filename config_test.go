package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIPInRange(t *testing.T) {
	if !IPInRange("192.168.1.20", "192.168.1.10", "192.168.1.30") {
		t.Fatal("expected address to be in range")
	}
	if IPInRange("192.168.2.20", "192.168.1.10", "192.168.1.30") {
		t.Fatal("expected address outside range")
	}
	if IPInRange("not-an-ip", "192.168.1.10", "192.168.1.30") {
		t.Fatal("invalid address must not match")
	}
}

func TestDeviceGroup(t *testing.T) {
	Groups = GroupConfig{
		"office": {"192.168.1.10", "192.168.2.10-192.168.2.20"},
	}

	if got := DeviceGroup("192.168.1.10"); got != "office" {
		t.Fatalf("exact match: got %q", got)
	}
	if got := DeviceGroup("192.168.2.15"); got != "office" {
		t.Fatalf("range match: got %q", got)
	}
	if got := DeviceGroup("10.0.0.1"); got != "UNKNOWN" {
		t.Fatalf("unexpected match: got %q", got)
	}
}

func TestIsLocalExporterIP(t *testing.T) {
	if !IsLocalExporterIP("192.168.10.238", "192.168.10.238") {
		t.Fatal("expected matching exporter IP")
	}
	if IsLocalExporterIP("192.168.10.238", "192.168.10.239") {
		t.Fatal("expected exporter IP mismatch")
	}
}

func TestLoadConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := `{
		"groups": {"office": ["192.168.1.10"]},
		"interfaces": {
			"2": {"direction": "upload"},
			"7": {"direction": "download", "isp_name": "EXAMPLE ISP"}
		}
	}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}

	if err := LoadConfig(path); err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got := Interfaces[2].Direction; got != "upload" {
		t.Fatalf("interface 2 direction = %q", got)
	}
	if got := Interfaces[7].ISPName; got != "EXAMPLE ISP" {
		t.Fatalf("interface 7 ISP = %q", got)
	}
	if got := DeviceGroup("192.168.1.10"); got != "office" {
		t.Fatalf("loaded group = %q", got)
	}
}

func TestLoadConfigRequiresISPForDownload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := `{"groups": {}, "interfaces": {"7": {"direction": "download"}}}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}

	if err := LoadConfig(path); err == nil {
		t.Fatal("expected missing ISP name to be rejected")
	}
}
