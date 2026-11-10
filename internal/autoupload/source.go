package autoupload

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/naming"
	"github.com/Aryan-mor/jellyfin-telegram/internal/seerr"
	"github.com/Aryan-mor/jellyfin-telegram/internal/stack"
)

// Source reads imported files from Radarr and Sonarr and cleans them up afterwards.
type Source struct {
	Downloads  string
	RadarrBase string
	RadarrKey  string
	SonarrBase string
	SonarrKey  string
	SeerrBase  string
	SeerrKey   string
	Qbit       *stack.Qbit
	HTTP       *http.Client
}

func (s *Source) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 2 * time.Minute}
}

func (s *Source) List(ctx context.Context) ([]Item, error) {
	var out []Item
	if s.RadarrBase != "" {
		movies, err := s.movies(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, movies...)
	}
	if s.SonarrBase != "" {
		episodes, err := s.episodes(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, episodes...)
	}
	return out, nil
}

func (s *Source) movies(ctx context.Context) ([]Item, error) {
	var list []movie
	if err := s.get(ctx, s.RadarrBase, s.RadarrKey, "/api/v3/movie", &list); err != nil {
		return nil, err
	}
	var out []Item
	for _, m := range list {
		if m.MovieFile == nil || m.MovieFile.Path == "" {
			continue
		}
		label := labelOf(m.MovieFile.Quality.Quality.Name)
		file, caption := naming.UploadName(naming.Parsed{Title: m.Title, Year: m.Year, TMDBID: m.TmdbID, Label: label}, filepath.Ext(m.MovieFile.Path))
		out = append(out, Item{
			Kind: "movie", RemoteID: m.MovieFile.ID, OwnerID: m.ID,
			Path: hostPath(s.Downloads, m.MovieFile.Path), Size: m.MovieFile.Size,
			FileName: file, Caption: caption,
		})
	}
	return out, nil
}

func (s *Source) episodes(ctx context.Context) ([]Item, error) {
	var series []seriesInfo
	if err := s.get(ctx, s.SonarrBase, s.SonarrKey, "/api/v3/series", &series); err != nil {
		return nil, err
	}
	var out []Item
	for _, show := range series {
		if show.Statistics != nil && show.Statistics.EpisodeFileCount == 0 {
			continue
		}
		var files []episodeFile
		if err := s.get(ctx, s.SonarrBase, s.SonarrKey, fmt.Sprintf("/api/v3/episodefile?seriesId=%d", show.ID), &files); err != nil {
			return nil, err
		}
		var episodes []episodeInfo
		if err := s.get(ctx, s.SonarrBase, s.SonarrKey, fmt.Sprintf("/api/v3/episode?seriesId=%d", show.ID), &episodes); err != nil {
			return nil, err
		}
		first := map[int]episodeInfo{}
		for _, ep := range episodes {
			if ep.EpisodeFileID == 0 {
				continue
			}
			cur, ok := first[ep.EpisodeFileID]
			if !ok || ep.EpisodeNumber < cur.EpisodeNumber {
				first[ep.EpisodeFileID] = ep
			}
		}
		for _, file := range files {
			ep := first[file.ID]
			if ep.ID == 0 {
				// The episode list does not name this file yet. Guessing an id could unmonitor
				// the wrong episode, so the file waits for the next pass.
				continue
			}
			name, caption := naming.UploadName(naming.Parsed{
				Title: show.Title, Year: show.Year, TMDBID: show.TmdbID, Episode: true,
				Season: file.SeasonNumber, Number: ep.EpisodeNumber,
			}, filepath.Ext(file.Path))
			out = append(out, Item{
				Kind: "episode", RemoteID: file.ID, OwnerID: ep.ID,
				Path: hostPath(s.Downloads, file.Path), Size: file.Size,
				FileName: name, Caption: caption,
			})
		}
	}
	return out, nil
}

func (s *Source) Scan(ctx context.Context) error {
	if s.SeerrBase == "" {
		return fmt.Errorf("Seerr is not running, so the file stays on disk")
	}
	return seerr.ScanLibrary(ctx, s.SeerrBase, s.SeerrKey)
}

func (s *Source) Unmonitor(ctx context.Context, item Item) error {
	if item.Kind == "episode" {
		return s.send(ctx, s.SonarrBase, s.SonarrKey, http.MethodPut, "/api/v3/episode/monitor", map[string]any{
			"episodeIds": s.episodesOfFile(ctx, item), "monitored": false,
		}, nil)
	}
	return s.send(ctx, s.RadarrBase, s.RadarrKey, http.MethodPut, "/api/v3/movie/editor", map[string]any{
		"movieIds": []int{item.OwnerID}, "monitored": false,
	}, nil)
}

// episodesOfFile lists every episode one file covers, so a file that holds two episodes does not
// leave the second one monitored and missing. It falls back to the single episode seen at import.
func (s *Source) episodesOfFile(ctx context.Context, item Item) []int {
	var episodes []episodeInfo
	err := s.get(ctx, s.SonarrBase, s.SonarrKey, fmt.Sprintf("/api/v3/episode?episodeFileId=%d", item.RemoteID), &episodes)
	ids := []int{item.OwnerID}
	if err != nil {
		return ids
	}
	for _, ep := range episodes {
		// Only episodes that really belong to this file count, in case the filter was ignored.
		if ep.EpisodeFileID == item.RemoteID && ep.ID != item.OwnerID {
			ids = append(ids, ep.ID)
		}
	}
	return ids
}

// DeleteFile removes the imported copy. A file that is already gone counts as deleted, so a retry
// after a half-finished cleanup does not get stuck.
func (s *Source) DeleteFile(ctx context.Context, item Item) error {
	var err error
	if item.Kind == "episode" {
		err = s.send(ctx, s.SonarrBase, s.SonarrKey, http.MethodDelete, fmt.Sprintf("/api/v3/episodefile/%d", item.RemoteID), nil, nil)
	} else {
		err = s.send(ctx, s.RadarrBase, s.RadarrKey, http.MethodDelete, fmt.Sprintf("/api/v3/moviefile/%d", item.RemoteID), nil, nil)
	}
	if err != nil && strings.Contains(err.Error(), "status 404") {
		return nil
	}
	return err
}

// DownloadID finds the torrent a file came from in the grab history. A record only counts when it
// names this movie or episode, so a filter the server ignores cannot point at another torrent.
func (s *Source) DownloadID(ctx context.Context, item Item) (string, error) {
	base, key, filter := s.RadarrBase, s.RadarrKey, fmt.Sprintf("movieIds=%d", item.OwnerID)
	if item.Kind == "episode" {
		base, key, filter = s.SonarrBase, s.SonarrKey, fmt.Sprintf("episodeId=%d", item.OwnerID)
	}
	if base == "" {
		return "", nil
	}
	var page struct {
		Records []historyRecord `json:"records"`
	}
	path := "/api/v3/history?page=1&pageSize=50&sortKey=date&sortDirection=descending&eventType=1&" + filter
	if err := s.get(ctx, base, key, path, &page); err != nil {
		return "", err
	}
	sort.SliceStable(page.Records, func(i, j int) bool { return page.Records[i].Date > page.Records[j].Date })
	for _, rec := range page.Records {
		owner := rec.MovieID
		if item.Kind == "episode" {
			owner = rec.EpisodeID
		}
		if owner == item.OwnerID && rec.DownloadID != "" {
			return rec.DownloadID, nil
		}
	}
	return "", nil
}

func (s *Source) DeleteTorrent(ctx context.Context, hash string) error {
	if s.Qbit == nil || hash == "" {
		return nil
	}
	// Radarr and Sonarr write the hash in capitals, qBittorrent in lower case.
	return s.Qbit.Delete(ctx, strings.ToLower(hash))
}

func (s *Source) Pause(ctx context.Context) error {
	if s.Qbit == nil {
		return nil
	}
	return s.Qbit.PauseAll(ctx)
}

func (s *Source) Resume(ctx context.Context) error {
	if s.Qbit == nil {
		return nil
	}
	return s.Qbit.ResumeAll(ctx)
}

type movie struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	TmdbID    int    `json:"tmdbId"`
	MovieFile *struct {
		ID      int    `json:"id"`
		Path    string `json:"path"`
		Size    int64  `json:"size"`
		Quality struct {
			Quality struct {
				Name string `json:"name"`
			} `json:"quality"`
		} `json:"quality"`
	} `json:"movieFile"`
}

type seriesInfo struct {
	ID         int    `json:"id"`
	Title      string `json:"title"`
	Year       int    `json:"year"`
	TmdbID     int    `json:"tmdbId"`
	Statistics *struct {
		EpisodeFileCount int `json:"episodeFileCount"`
	} `json:"statistics"`
}

type historyRecord struct {
	DownloadID string `json:"downloadId"`
	MovieID    int    `json:"movieId"`
	EpisodeID  int    `json:"episodeId"`
	Date       string `json:"date"`
}

type episodeFile struct {
	ID           int    `json:"id"`
	SeasonNumber int    `json:"seasonNumber"`
	Path         string `json:"path"`
	Size         int64  `json:"size"`
}

type episodeInfo struct {
	ID            int `json:"id"`
	EpisodeNumber int `json:"episodeNumber"`
	EpisodeFileID int `json:"episodeFileId"`
}

func labelOf(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "1080"):
		return "1080p"
	case strings.Contains(n, "720"):
		return "720p"
	case strings.Contains(n, "480") || strings.Contains(n, "576"):
		return "480p"
	default:
		return ""
	}
}

func hostPath(downloads, containerPath string) string {
	if downloads == "" || !strings.HasPrefix(containerPath, "/data/") {
		return containerPath
	}
	return filepath.Join(downloads, filepath.FromSlash(strings.TrimPrefix(containerPath, "/data/")))
}

func (s *Source) get(ctx context.Context, base, key, path string, out any) error {
	return s.send(ctx, base, key, http.MethodGet, path, nil, out)
}

func (s *Source) post(ctx context.Context, base, key, path string, body any) error {
	return s.send(ctx, base, key, http.MethodPost, path, body, nil)
}

func (s *Source) send(ctx context.Context, base, key, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = strings.NewReader(string(raw))
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, reader)
	if err != nil {
		return err
	}
	if key != "" {
		if strings.Contains(path, "/api/v1/") {
			req.Header.Set("X-Api-Key", key)
		} else {
			req.Header.Set("X-Api-Key", key)
		}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode >= 300 {
		text := strings.TrimSpace(string(payload))
		if len(text) > 200 {
			text = text[:200]
		}
		return fmt.Errorf("%s %s: status %d (%s)", method, path, res.StatusCode, text)
	}
	if out != nil && len(payload) > 0 {
		return json.Unmarshal(payload, out)
	}
	return nil
}
