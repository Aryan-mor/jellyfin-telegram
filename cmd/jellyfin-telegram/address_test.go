package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/managed"
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

func TestSignInDetails(t *testing.T) {
	managedCreds := managed.Credentials{User: "admin", Password: "pw"}
	got := signInDetails(config.Config{}, managedCreds, true)
	if len(got) != 2 || got[0].App != "Jellyfin" || got[0].User != "admin" || got[0].Password != "pw" || got[1].App != "Seerr" || got[1].User != "admin" {
		t.Fatalf("managed: %+v", got)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "seerr"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "seerr", "jellyfin.json"), []byte(`{"user":"seerr","password":"s3"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	own := signInDetails(config.Config{DataDir: dir}, managed.Credentials{}, true)
	if len(own) != 2 || own[0].User != "" || own[1].User != "seerr" || own[1].Password != "s3" {
		t.Fatalf("own Jellyfin: %+v", own)
	}
	if none := signInDetails(config.Config{DataDir: t.TempDir()}, managed.Credentials{}, true); none[1].User != "" {
		t.Fatalf("an account that was never made was shown: %+v", none[1])
	}
}
