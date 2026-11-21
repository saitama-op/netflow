package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
)

type Flow struct {
	SamplerAddress string  `json:"sampler_address"`
	SrcAddr        string  `json:"src_addr"`
	DstAddr        string  `json:"dst_addr"`
	Proto          string  `json:"proto"`
	SrcPort        int     `json:"src_port"`
	DstPort        int     `json:"dst_port"`
	Bytes          float64 `json:"bytes"`
	InIf           int     `json:"in_if"`
}

type GroupConfig map[string][]string

type InterfaceConfig struct {
	Direction string `json:"direction"`
	ISPName   string `json:"isp_name,omitempty"`
}

type AppConfig struct {
	Groups     GroupConfig             `json:"groups"`
	Interfaces map[int]InterfaceConfig `json:"interfaces"`
}

var Groups GroupConfig
var Interfaces map[int]InterfaceConfig

func LoadConfig(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var config AppConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}
	if config.Groups == nil {
		return fmt.Errorf("configuration must include a groups object")
	}
	if len(config.Interfaces) == 0 {
		return fmt.Errorf("configuration must include at least one interface")
	}
	for inIf, iface := range config.Interfaces {
		if inIf < 0 {
			return fmt.Errorf("interface index must not be negative: %d", inIf)
		}
		switch iface.Direction {
		case "upload":
		case "download":
			if strings.TrimSpace(iface.ISPName) == "" {
				return fmt.Errorf("download interface %d must include isp_name", inIf)
			}
		default:
			return fmt.Errorf("interface %d has invalid direction %q; expected upload or download", inIf, iface.Direction)
		}
	}

	Groups = config.Groups
	Interfaces = config.Interfaces
	return nil
}

func IPInRange(ipStr, startStr, endStr string) bool {
	ip := net.ParseIP(ipStr).To4()
	start := net.ParseIP(startStr).To4()
	end := net.ParseIP(endStr).To4()

	if ip == nil || start == nil || end == nil {
		return false
	}
	return bytes.Compare(ip, start) >= 0 && bytes.Compare(ip, end) <= 0
}

func DeviceGroup(ip string) string {
	if ip == "UNKNOWN_DEVICE" || ip == "" {
		return "UNKNOWN"
	}
	for group, entries := range Groups {
		for _, entry := range entries {
			if strings.Contains(entry, "-") {
				parts := strings.SplitN(entry, "-", 2)
				if len(parts) == 2 && IPInRange(ip, strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])) {
					return group
				}
			} else {
				if ip == entry {
					return group
				}
			}
		}
	}
	return "UNKNOWN"
}

func IsInternalTraffic(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast()
}

func IsLocalExporterIP(ipStr, exporterIP string) bool {
	return ipStr == exporterIP
}
