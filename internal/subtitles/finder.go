package subtitles

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// maxPackAttempts is how many ranked packs are tried, since a hit may hold no usable file.
const maxPackAttempts = 3

// Provider is a source of subtitle packs.
type Provider interface {
	Search(ctx context.Context, query string) ([]Pack, error)
	Download(ctx context.Context, tag string) ([]SubFile, error)
}

// Query describes the video a subtitle is wanted for.
type Query struct {
	IMDb     string
	Title    string
	Year     int
	Season   int // zero for a movie
	Episode  int // zero for a movie
	FileName string
	// Seconds is the running time of the video, zero when unknown.
	Seconds int
}

// Find returns the best subtitle, or nil when no pack holds a matching file.
func Find(ctx context.Context, p Provider, q Query) (*SubFile, error) {
	packs, err := search(ctx, p, q)
	if err != nil {
		return nil, err
	}
	ranked := rank(packs, q)
	var lastErr error
	for i, pack := range ranked {
		if i >= maxPackAttempts {
			break
		}
		files, err := p.Download(ctx, pack.Tag)
		if err != nil {
			lastErr = err
			continue
		}
		if len(files) == 0 {
			continue
		}
		var pick *SubFile
		if q.Season > 0 && q.Episode > 0 {
			pick = pickEpisode(files, q.Season, q.Episode)
		} else {
			pick = pickMovie(files, q.FileName)
		}
		if pick != nil && fitsRuntime(*pick, q.Seconds) {
			return pick, nil
		}
	}
	return nil, lastErr
}

func search(ctx context.Context, p Provider, q Query) ([]Pack, error) {
	if id := imdbQuery(q.IMDb); id != "" {
		packs, err := p.Search(ctx, id)
		if err != nil {
			return nil, err
		}
		// The search is fuzzy even for an id: tt0111301 also returns tt0111701 and tt0117301,
		// which are different films. Only the id on the pack proves the match.
		var exact []Pack
		for _, pack := range packs {
			if imdbQuery(pack.IMDb) == id {
				exact = append(exact, pack)
			}
		}
		if len(exact) > 0 {
			return exact, nil
		}
	}
	if strings.TrimSpace(q.Title) == "" {
		return nil, nil
	}
	packs, err := p.Search(ctx, q.Title)
	if err != nil {
		return nil, err
	}
	// Free text is fuzzy, so keep only packs naming this exact title and year.
	var out []Pack
	for _, pack := range packs {
		if titleMatches(pack, q) && (q.IMDb == "" || pack.IMDb == "" || imdbQuery(pack.IMDb) == imdbQuery(q.IMDb)) {
			out = append(out, pack)
		}
	}
	return out, nil
}

// rank drops packs of the wrong kind or season and orders the rest by release similarity.
func rank(packs []Pack, q Query) []Pack {
	isEpisode := q.Season > 0 && q.Episode > 0
	type scored struct {
		pack  Pack
		score int
	}
	var list []scored
	for _, pack := range packs {
		if pack.Series != isEpisode {
			continue
		}
		score := 0
		for _, rel := range pack.Releases {
			score = max(score, tokenOverlap(rel, q.FileName))
		}
		if isEpisode {
			season, known := packSeason(pack)
			if known && season != q.Season {
				continue
			}
			if known {
				score += 50
			}
		}
		list = append(list, scored{pack, score})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
	out := make([]Pack, len(list))
	for i, s := range list {
		out[i] = s.pack
	}
	return out
}

var reLastCue = regexp.MustCompile(`(\d+):(\d\d):(\d\d)[,.]\d+\s*-->`)

// fitsRuntime rejects a subtitle that cannot belong to this video. Last cues usually end a little
// before the credits. A much earlier end is a partial file, and a later one is another cut. Only
// .srt timing is read, so other formats and an unknown running time pass.
func fitsRuntime(f SubFile, seconds int) bool {
	if seconds <= 0 || f.Ext != ".srt" {
		return true
	}
	cues := reLastCue.FindAllStringSubmatch(f.Text, -1)
	if len(cues) == 0 {
		return false
	}
	last := cues[len(cues)-1]
	h, _ := strconv.Atoi(last[1])
	m, _ := strconv.Atoi(last[2])
	s, _ := strconv.Atoi(last[3])
	end := float64(h*3600 + m*60 + s)
	return end >= 0.6*float64(seconds) && end <= 1.15*float64(seconds)
}

var ordinals = map[string]int{
	"first": 1, "second": 2, "third": 3, "fourth": 4, "fifth": 5, "sixth": 6, "seventh": 7,
	"eighth": 8, "ninth": 9, "tenth": 10, "eleventh": 11, "twelfth": 12, "thirteenth": 13,
	"fourteenth": 14, "fifteenth": 15,
}

var reOrdinalSeason = regexp.MustCompile(`(?i)\b([a-z]+)\s+season\b`)

// packSeason reads the season from a title like "Name - First Season (2018)", then releases.
func packSeason(p Pack) (int, bool) {
	if m := reOrdinalSeason.FindStringSubmatch(p.Title); m != nil {
		if n, ok := ordinals[strings.ToLower(m[1])]; ok {
			return n, true
		}
	}
	if se := parseSeasonEpisode(p.Title); se.hasS {
		return se.season, true
	}
	for _, rel := range p.Releases {
		if se := parseSeasonEpisode(rel); se.hasS {
			return se.season, true
		}
	}
	return 0, false
}

func titleMatches(p Pack, q Query) bool {
	if q.Year > 0 && p.Year != "" && p.Year != strconv.Itoa(q.Year) {
		return false
	}
	title := p.Title
	if i := strings.Index(title, " - "); i > 0 {
		title = title[:i]
	}
	if i := strings.LastIndex(title, "("); i > 0 {
		title = title[:i]
	}
	return normalizeTitle(title) == normalizeTitle(q.Title)
}

func normalizeTitle(s string) string {
	return strings.Join(tokens(s), " ")
}

func imdbQuery(id string) string {
	s := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(id)), "tt")
	if s == "" {
		return ""
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return "tt" + s
}

var reToken = regexp.MustCompile(`[\p{L}\p{N}]+`)

func tokens(s string) []string {
	return reToken.FindAllString(strings.ToLower(s), -1)
}

// tokenOverlap counts release tokens two names share, such as the group or source.
func tokenOverlap(a, b string) int {
	set := map[string]bool{}
	for _, t := range tokens(a) {
		set[t] = true
	}
	n := 0
	for _, t := range tokens(b) {
		if set[t] {
			n++
			delete(set, t)
		}
	}
	return n
}
