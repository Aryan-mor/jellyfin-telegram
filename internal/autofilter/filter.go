// Package autofilter decides which automatically added titles are kept. A person picks genres,
// origin countries, original languages and streaming networks to include or exclude. A title a
// person asked for in Seerr never passes through it.
package autofilter

import "strings"

// DefaultGenres are the genres a title may have when no part of the filter is set.
var DefaultGenres = []string{
	"Action", "Adventure", "Animation", "Comedy", "Crime", "Documentary", "Drama", "Family", "Fantasy",
	"History", "Horror", "Music", "Mystery", "Romance", "Science Fiction", "Thriller", "War", "Western",
	"Action & Adventure", "Sci-Fi & Fantasy", "War & Politics", "Kids",
}

// Rule is one part of the filter. Values are compared without regard to case.
type Rule struct {
	Include []string `json:"include"`
	Exclude []string `json:"exclude"`
}

// Empty reports that the rule has no values.
func (r Rule) Empty() bool { return len(r.Include) == 0 && len(r.Exclude) == 0 }

type verdict int

const (
	neutral verdict = iota
	positive
	reject
)

// judge rates one part. A title with an excluded value is rejected. With an include list, a match
// is positive and no match is rejected. Otherwise the part has no say.
func (r Rule) judge(values []string) verdict {
	if r.Empty() {
		return neutral
	}
	if anyIn(r.Exclude, values) {
		return reject
	}
	if len(r.Include) == 0 {
		return neutral
	}
	if anyIn(r.Include, values) {
		return positive
	}
	return reject
}

func anyIn(list, values []string) bool {
	for _, v := range values {
		for _, x := range list {
			if strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(v)) {
				return true
			}
		}
	}
	return false
}

// Filter is what auto-added content may be. The parts are joined with OR: a title positive in one
// part is kept even when another part rejects it. With no part positive, any rejection drops it.
type Filter struct {
	// AllowAdult lets titles flagged as adult, or with an erotic genre, through. It is checked
	// before the parts, so no include can override it.
	AllowAdult bool `json:"allow_adult"`
	// Genres are English TMDB genre names.
	Genres Rule `json:"genres"`
	// Countries are ISO 3166-1 alpha-2 codes of the origin country.
	Countries Rule `json:"countries"`
	// Languages are ISO 639-1 codes of the original language.
	Languages Rule `json:"languages"`
	// Networks are ids from Networks.
	Networks Rule `json:"networks"`
}

// Configured reports that at least one part has values. An unconfigured filter keeps the old
// behaviour: only titles with one of DefaultGenres pass.
func (f Filter) Configured() bool {
	return !f.Genres.Empty() || !f.Countries.Empty() || !f.Languages.Empty() || !f.Networks.Empty()
}

// Title is what is known about one title. A nil Countries or Networks means not known yet; Allows
// may then ask for more.
type Title struct {
	Adult     bool
	Genres    []string
	Countries []string
	Languages []string
	Networks  []string
}

// Allows reports whether an auto-added title is kept. more fetches the countries and networks
// that are not known yet. It is called at most once, and only when they can change the answer.
// Values still unknown after it count as none, so that part cannot be positive.
func (f Filter) Allows(t Title, more func() Title) bool {
	if !f.AllowAdult && (t.Adult || erotic(t.Genres)) {
		return false
	}
	f = f.effective()
	if more != nil && f.needsMore(t) {
		d := more()
		if t.Countries == nil {
			t.Countries = d.Countries
		}
		if t.Networks == nil {
			t.Networks = d.Networks
		}
	}
	return combine(f.verdicts(t))
}

func erotic(genres []string) bool {
	for _, g := range genres {
		if strings.Contains(strings.ToLower(g), "erotic") {
			return true
		}
	}
	return false
}

// effective fills in DefaultGenres when nothing is set.
func (f Filter) effective() Filter {
	if !f.Configured() {
		f.Genres.Include = DefaultGenres
	}
	return f
}

type part struct {
	rule   Rule
	values []string
	known  bool
}

func (f Filter) parts(t Title) []part {
	return []part{
		{f.Genres, t.Genres, true},
		{f.Languages, t.Languages, true},
		{f.Countries, t.Countries, t.Countries != nil},
		{f.Networks, t.Networks, t.Networks != nil},
	}
}

func (f Filter) verdicts(t Title) []verdict {
	var out []verdict
	for _, p := range f.parts(t) {
		out = append(out, p.rule.judge(p.values))
	}
	return out
}

func combine(vs []verdict) bool {
	rejected := false
	for _, v := range vs {
		if v == positive {
			return true
		}
		if v == reject {
			rejected = true
		}
	}
	return !rejected
}

// needsMore reports whether the unknown parts can still change the answer.
func (f Filter) needsMore(t Title) bool {
	rejected, pending, mayPass := false, false, false
	for _, p := range f.parts(t) {
		if !p.known {
			if !p.rule.Empty() {
				pending = true
				mayPass = mayPass || len(p.rule.Include) > 0
			}
			continue
		}
		switch p.rule.judge(p.values) {
		case positive:
			return false
		case reject:
			rejected = true
		}
	}
	// An exclude-only part can only reject, which changes nothing once another part has.
	return pending && (mayPass || !rejected)
}

// Network is a streaming brand. Series are matched by TMDB network id, films by TMDB watch
// provider id (flat-rate streaming in any region).
type Network struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	TV        []int  `json:"-"`
	Providers []int  `json:"-"`
}

// Networks are the brands a person can pick. The TMDB ids are assumed from TMDB's public data and
// may need updating when a brand changes.
var Networks = []Network{
	{ID: "netflix", Name: "Netflix", TV: []int{213}, Providers: []int{8}},
	{ID: "prime_video", Name: "Prime Video", TV: []int{1024}, Providers: []int{9, 119}},
	{ID: "disney_plus", Name: "Disney+", TV: []int{2739}, Providers: []int{337}},
	{ID: "apple_tv_plus", Name: "Apple TV+", TV: []int{2552}, Providers: []int{350}},
	{ID: "hbo_max", Name: "HBO / Max", TV: []int{49, 3186}, Providers: []int{1899, 384}},
	{ID: "hulu", Name: "Hulu", TV: []int{453}, Providers: []int{15}},
	{ID: "paramount_plus", Name: "Paramount+", TV: []int{4330}, Providers: []int{531}},
}

// NetworksOf maps TMDB ids to brand ids. tv picks network ids, otherwise watch provider ids. The
// result is never nil, so the networks count as known.
func NetworksOf(ids []int, tv bool) []string {
	out := []string{}
	for _, n := range Networks {
		list := n.Providers
		if tv {
			list = n.TV
		}
		if hasInt(list, ids) {
			out = append(out, n.ID)
		}
	}
	return out
}

func hasInt(list, ids []int) bool {
	for _, a := range list {
		for _, b := range ids {
			if a == b {
				return true
			}
		}
	}
	return false
}
