package autofilter

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/Aryan-mor/jellyfin-telegram/internal/setup"
)

// Server serves the filter to the panel and to the setup page.
type Server struct {
	EnvPath string
	// Genres lists TMDB's genre names. Nil, an error or an empty list falls back to DefaultGenres.
	Genres func(ctx context.Context) ([]string, error)
}

// View is what GET returns.
type View struct {
	Filter Filter `json:"filter"`
	// Configured is false while no part is set. DefaultGenres then decide.
	Configured    bool     `json:"configured"`
	DefaultGenres []string `json:"default_genres"`
	Options       Options  `json:"options"`
}

// Options are the values each part can hold.
type Options struct {
	Genres []Option `json:"genres"`
	// GenresSource is "tmdb" when the genres came from TMDB, otherwise "default".
	GenresSource string   `json:"genres_source"`
	Countries    []Option `json:"countries"`
	Languages    []Option `json:"languages"`
	Networks     []Option `json:"networks"`
}

// Mount adds GET and POST prefix/auto-filter behind guard.
func (s *Server) Mount(mux *http.ServeMux, prefix string, guard func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET "+prefix+"/auto-filter", guard(s.handleGet))
	mux.HandleFunc("POST "+prefix+"/auto-filter", guard(s.handlePost))
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	f := Load(s.EnvPath)
	v := View{Filter: f, Configured: f.Configured(), DefaultGenres: DefaultGenres}
	names, source := append([]string{}, DefaultGenres...), "default"
	if s.Genres != nil {
		if list, err := s.Genres(r.Context()); err == nil && len(list) > 0 {
			names, source = list, "tmdb"
		}
	}
	// A saved genre missing from the list stays visible, so it can be removed.
	for _, g := range append(append([]string{}, f.Genres.Include...), f.Genres.Exclude...) {
		if !anyIn(names, []string{g}) {
			names = append(names, g)
		}
	}
	v.Options = Options{
		Genres: GenreOptions(names), GenresSource: source,
		Countries: Countries(), Languages: Languages(), Networks: NetworkOptions(),
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handlePost(w http.ResponseWriter, r *http.Request) {
	var body Filter
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	f, err := body.Clean()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := setup.UpdateEnv(s.EnvPath, f.Env()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot write the settings file: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "filter": f, "configured": f.Configured()})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
