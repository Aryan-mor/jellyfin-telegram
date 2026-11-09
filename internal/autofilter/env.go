package autofilter

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// Settings file keys. Each holds a comma separated list. TRENDING_GENRES, TRENDING_EXCLUDE_GENRES
// and TRENDING_ALLOW_ADULT are read when the newer key is missing from the file and environment.
const (
	KeyAllowAdult       = "AUTO_FILTER_ALLOW_ADULT"
	KeyGenres           = "AUTO_FILTER_GENRES"
	KeyExcludeGenres    = "AUTO_FILTER_EXCLUDE_GENRES"
	KeyCountries        = "AUTO_FILTER_COUNTRIES"
	KeyExcludeCountries = "AUTO_FILTER_EXCLUDE_COUNTRIES"
	KeyLanguages        = "AUTO_FILTER_LANGUAGES"
	KeyExcludeLanguages = "AUTO_FILTER_EXCLUDE_LANGUAGES"
	KeyNetworks         = "AUTO_FILTER_NETWORKS"
	KeyExcludeNetworks  = "AUTO_FILTER_EXCLUDE_NETWORKS"

	legacyAllowAdult    = "TRENDING_ALLOW_ADULT"
	legacyGenres        = "TRENDING_GENRES"
	legacyExcludeGenres = "TRENDING_EXCLUDE_GENRES"
)

// Load reads the filter from the settings file. A key in the file wins over the environment, so a
// change saved by the panel applies without a restart.
func Load(envPath string) Filter {
	m, _ := godotenv.Read(envPath)
	return FromLookup(func(key string) (string, bool) {
		if v, ok := m[key]; ok {
			return v, true
		}
		return os.LookupEnv(key)
	})
}

// FromLookup builds the filter from settings looked up by key.
func FromLookup(lookup func(string) (string, bool)) Filter {
	get := func(key, legacy string) string {
		if v, ok := lookup(key); ok {
			return v
		}
		if legacy != "" {
			v, _ := lookup(legacy)
			return v
		}
		return ""
	}
	list := func(key, legacy string) []string { return splitList(get(key, legacy)) }
	adult := strings.TrimSpace(get(KeyAllowAdult, legacyAllowAdult))
	return Filter{
		AllowAdult: strings.EqualFold(adult, "true") || adult == "1",
		Genres:     Rule{Include: list(KeyGenres, legacyGenres), Exclude: list(KeyExcludeGenres, legacyExcludeGenres)},
		Countries:  Rule{Include: list(KeyCountries, ""), Exclude: list(KeyExcludeCountries, "")},
		Languages:  Rule{Include: list(KeyLanguages, ""), Exclude: list(KeyExcludeLanguages, "")},
		Networks:   Rule{Include: list(KeyNetworks, ""), Exclude: list(KeyExcludeNetworks, "")},
	}
}

// Env is the filter as settings file lines. Every key is written, so the old TRENDING_* keys stop
// counting once a person saves.
func (f Filter) Env() map[string]string {
	join := func(v []string) string { return strings.Join(v, ",") }
	return map[string]string{
		KeyAllowAdult:       fmt.Sprint(f.AllowAdult),
		KeyGenres:           join(f.Genres.Include),
		KeyExcludeGenres:    join(f.Genres.Exclude),
		KeyCountries:        join(f.Countries.Include),
		KeyExcludeCountries: join(f.Countries.Exclude),
		KeyLanguages:        join(f.Languages.Include),
		KeyExcludeLanguages: join(f.Languages.Exclude),
		KeyNetworks:         join(f.Networks.Include),
		KeyExcludeNetworks:  join(f.Networks.Exclude),
	}
}

func splitList(csv string) []string {
	out := []string{}
	for _, p := range strings.Split(csv, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Clean checks what a person submitted and puts the codes in their usual case. Genres are free
// text, as TMDB may name more than the default list; the other parts must come from their lists.
func (f Filter) Clean() (Filter, error) {
	countries := ids(Countries())
	languages := ids(Languages())
	networks := ids(NetworkOptions())
	var err error
	if f.Genres, err = cleanRule("genre", f.Genres, nil, strings.TrimSpace); err != nil {
		return Filter{}, err
	}
	if f.Countries, err = cleanRule("country", f.Countries, countries, strings.ToUpper); err != nil {
		return Filter{}, err
	}
	if f.Languages, err = cleanRule("language", f.Languages, languages, strings.ToLower); err != nil {
		return Filter{}, err
	}
	if f.Networks, err = cleanRule("network", f.Networks, networks, strings.ToLower); err != nil {
		return Filter{}, err
	}
	return f, nil
}

func ids(opts []Option) map[string]bool {
	m := map[string]bool{}
	for _, o := range opts {
		m[o.ID] = true
	}
	return m
}

func cleanRule(what string, r Rule, known map[string]bool, norm func(string) string) (Rule, error) {
	clean := func(list []string) ([]string, error) {
		out := []string{}
		seen := map[string]bool{}
		for _, v := range list {
			v = norm(strings.TrimSpace(v))
			if v == "" {
				continue
			}
			if strings.ContainsAny(v, ",\r\n") {
				return nil, fmt.Errorf("%s %q holds a comma or a line break", what, v)
			}
			if known != nil && !known[v] {
				return nil, fmt.Errorf("unknown %s %q", what, v)
			}
			if !seen[strings.ToLower(v)] {
				seen[strings.ToLower(v)] = true
				out = append(out, v)
			}
		}
		return out, nil
	}
	inc, err := clean(r.Include)
	if err != nil {
		return Rule{}, err
	}
	exc, err := clean(r.Exclude)
	if err != nil {
		return Rule{}, err
	}
	if anyIn(inc, exc) {
		return Rule{}, errors.New("a " + what + " cannot be both included and excluded")
	}
	return Rule{Include: inc, Exclude: exc}, nil
}
