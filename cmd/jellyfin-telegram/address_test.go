package main

import (
	"net"
	"testing"
)

func TestVirtualAdapter(t *testing.T) {
	hidden := []string{"vEthernet (WSL)", "docker0", "br-abc", "veth123", "virbr0", "WSL", "VMware Network Adapter", "VirtualBox Host-Only"}
	for _, name := range hidden {
		if !virtualAdapter(name) {
			t.Errorf("%q should be hidden", name)
		}
	}
	shown := []string{"Ethernet", "Wi-Fi", "Tailscale", "eth0"}
	for _, name := range shown {
		if virtualAdapter(name) {
			t.Errorf("%q should be listed", name)
		}
	}
}

func TestUsableAddress(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"192.168.2.75", true},
		{"10.1.2.3", true},
		{"172.16.0.1", true},
		{"100.122.54.9", true},
		{"100.64.0.1", true},
		{"100.127.255.254", true},
		{"100.63.255.255", false},
		{"100.128.0.1", false},
		{"8.8.8.8", false},
		{"127.0.0.1", false},
	}
	for _, tc := range cases {
		if got := usableAddress(net.ParseIP(tc.ip)); got != tc.want {
			t.Errorf("usableAddress(%s) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}
