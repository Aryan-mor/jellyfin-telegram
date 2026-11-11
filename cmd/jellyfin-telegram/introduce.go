package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/chanindex"
	"github.com/Aryan-mor/jellyfin-telegram/internal/hold"
)

const (
	introducedKey = "chanindex_introduced"
	introduceHold = "chanindex_introduce_hold"
)

// introduceChannels registers this library's public mirrors, direct channels and news channels
// with the channel index. A private channel is skipped. Each name is sent once per tag set.
func introduceChannels(ctx context.Context, cat *catalog.Catalog, idx *chanindex.Client, log *slog.Logger) {
	tick := time.NewTicker(15 * time.Minute)
	defer tick.Stop()
	for {
		if !hold.Stopped() {
			work, done := hold.Begin(ctx)
			err := introducePass(work, cat, idx, log)
			cancelled := work.Err() != nil
			done()
			if err != nil && ctx.Err() == nil && !cancelled && log != nil {
				log.Warn("channel index introduce", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-hold.Changed():
		}
	}
}

func introducePass(ctx context.Context, cat *catalog.Catalog, idx *chanindex.Client, log *slog.Logger) error {
	if cat == nil || idx == nil {
		return nil
	}
	hold, err := cat.StateValue(ctx, introduceHold)
	if err != nil {
		return err
	}
	if until, ok := parseHold(hold); ok && time.Now().Before(until) {
		return nil
	}
	want, err := publicIndexChannels(ctx, cat)
	if err != nil {
		return err
	}
	raw, err := cat.StateValue(ctx, introducedKey)
	if err != nil {
		return err
	}
	have := parseIntroduced(raw)
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if have[name] == want[name] {
			continue
		}
		err := idx.Introduce(ctx, name, strings.Split(want[name], ","))
		if errors.Is(err, chanindex.ErrLimited) {
			_ = cat.SetStateValue(ctx, introduceHold, fmt.Sprint(time.Now().Add(12*time.Hour).Unix()))
			return cat.SetStateValue(ctx, introducedKey, formatIntroduced(have))
		}
		if err != nil && !errors.Is(err, chanindex.ErrRejected) {
			_ = cat.SetStateValue(ctx, introducedKey, formatIntroduced(have))
			return err
		}
		if errors.Is(err, chanindex.ErrRejected) && log != nil {
			log.Warn("channel index refused a channel", "channel", name)
		} else if log != nil {
			log.Info("channel index introduced", "channel", name, "tags", want[name])
		}
		have[name] = want[name]
		if err := cat.SetStateValue(ctx, introducedKey, formatIntroduced(have)); err != nil {
			return err
		}
	}
	return nil
}

func publicIndexChannels(ctx context.Context, cat *catalog.Catalog) (map[string]string, error) {
	tags := map[string]map[string]bool{}
	mirrors, err := cat.Mirrors(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range mirrors {
		if m.Username == "" || m.Error != "" {
			continue
		}
		for _, tag := range mirrorIndexTags(m.Kinds) {
			addIndexTag(tags, m.Username, tag)
		}
	}
	sources, err := cat.Sources(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range sources {
		addIndexTag(tags, s.Username, "direct-file")
	}
	news, err := cat.NewsChannels(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range news {
		addIndexTag(tags, s.Username, "news")
	}
	out := make(map[string]string, len(tags))
	for name, set := range tags {
		list := make([]string, 0, len(set))
		for tag := range set {
			list = append(list, tag)
		}
		slices.Sort(list)
		out[name] = strings.Join(list, ",")
	}
	return out, nil
}

func addIndexTag(tags map[string]map[string]bool, username, tag string) {
	name := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(username), "@"))
	if name == "" || tag == "" {
		return
	}
	if tags[name] == nil {
		tags[name] = map[string]bool{}
	}
	tags[name][tag] = true
}

// mirrorIndexTags maps a mirror's parts onto the channel index slugs.
func mirrorIndexTags(kinds []string) []string {
	var out []string
	for _, k := range kinds {
		switch k {
		case "movie":
			out = append(out, "movie")
		case "series":
			out = append(out, "series")
		case "music":
			out = append(out, "music")
		case "video":
			out = append(out, "direct-file")
		}
	}
	slices.Sort(out)
	out = slices.Compact(out)
	return out
}

func parseIntroduced(raw string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		name, tags, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok && name != "" && tags != "" {
			out[name] = tags
		}
	}
	return out
}

func formatIntroduced(have map[string]string) string {
	names := make([]string, 0, len(have))
	for name := range have {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "%s %s\n", name, have[name])
	}
	return b.String()
}

func parseHold(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	var unix int64
	if _, err := fmt.Sscan(raw, &unix); err != nil || unix <= 0 {
		return time.Time{}, false
	}
	return time.Unix(unix, 0), true
}
