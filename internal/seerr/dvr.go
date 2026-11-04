package seerr

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
)

// DVR is one Radarr or Sonarr server Seerr should send requests to.
type DVR struct {
	// Kind is "radarr" or "sonarr".
	Kind                string
	Name                string
	Hostname            string
	Port                int
	APIKey              string
	BaseURL             string
	ExternalURL         string
	ProfileID           int
	ProfileName         string
	Directory           string
	MinimumAvailability string
}

const dvrName = "jellyfin-telegram"

// ApplyDVR creates or updates the Radarr and Sonarr entries. A second call keeps the same rows.
func ApplyDVR(ctx context.Context, base, apiKey string, servers []DVR) error {
	c := &client{base: trim(base), key: apiKey, http: &http.Client{}}
	for _, s := range servers {
		if err := applyOne(ctx, c, s); err != nil {
			return err
		}
	}
	return nil
}

// RemoveDVR deletes the entries this program created, so a request does not fail against a stopped server.
func RemoveDVR(ctx context.Context, base, apiKey string) error {
	c := &client{base: trim(base), key: apiKey, http: &http.Client{}}
	for _, kind := range []string{"radarr", "sonarr"} {
		list, err := listDVR(ctx, c, kind)
		if err != nil {
			return err
		}
		for _, item := range list {
			if item["name"] != dvrName {
				continue
			}
			id := int(num(item["id"]))
			if err := c.callOK(ctx, http.MethodDelete, "/api/v1/settings/"+kind+"/"+strconv.Itoa(id), nil, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func applyOne(ctx context.Context, c *client, s DVR) error {
	if s.Name == "" {
		s.Name = dvrName
	}
	body := map[string]any{
		"name": s.Name, "hostname": s.Hostname, "port": s.Port, "apiKey": s.APIKey,
		"useSsl": false, "baseUrl": s.BaseURL, "activeProfileId": s.ProfileID,
		"activeProfileName": s.ProfileName, "activeDirectory": s.Directory,
		"tags": []int{}, "is4k": false, "isDefault": true, "externalUrl": s.ExternalURL,
		"syncEnabled": true, "preventSearch": false, "tagRequests": false, "overrideRule": []int{},
	}
	if s.Kind == "sonarr" {
		body["seriesType"] = "standard"
		body["animeSeriesType"] = "anime"
		body["enableSeasonFolders"] = true
		body["monitorNewItems"] = "all"
	} else {
		avail := s.MinimumAvailability
		if avail == "" {
			avail = "released"
		}
		body["minimumAvailability"] = avail
	}
	list, err := listDVR(ctx, c, s.Kind)
	if err != nil {
		return err
	}
	for _, item := range list {
		if item["name"] != s.Name {
			continue
		}
		// The id is part of the address and Seerr refuses it in the body as read-only.
		id := int(num(item["id"]))
		return c.callOK(ctx, http.MethodPut, "/api/v1/settings/"+s.Kind+"/"+strconv.Itoa(id), body, nil)
	}
	return c.callOK(ctx, http.MethodPost, "/api/v1/settings/"+s.Kind, body, nil)
}

func listDVR(ctx context.Context, c *client, kind string) ([]map[string]any, error) {
	var list []map[string]any
	if err := c.callOK(ctx, http.MethodGet, "/api/v1/settings/"+kind, nil, &list); err != nil {
		return nil, fmt.Errorf("read Seerr %s: %w", kind, err)
	}
	return list, nil
}

func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	default:
		return 0
	}
}

func trim(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
