package seerr

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// ScanLibrary asks Seerr to look at what Jellyfin added recently, so a request can turn
// Available without waiting for Seerr's own timer. A Seerr that only has the older job
// name is asked for that one instead.
func ScanLibrary(ctx context.Context, base, key string) error {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" || strings.TrimSpace(key) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	c := &client{base: base, key: key, http: &http.Client{Timeout: 30 * time.Second}}
	err := c.callOK(ctx, http.MethodPost, "/api/v1/settings/jobs/jellyfin-recently-added-scan/run", nil, nil)
	if err != nil && strings.Contains(err.Error(), "404") {
		return c.callOK(ctx, http.MethodPost, "/api/v1/settings/jobs/availability-sync/run", nil, nil)
	}
	return err
}
