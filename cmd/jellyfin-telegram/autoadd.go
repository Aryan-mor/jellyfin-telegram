package main

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/chanindex"
	"github.com/Aryan-mor/jellyfin-telegram/internal/hold"
	"github.com/Aryan-mor/jellyfin-telegram/internal/newsfeed"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgsource"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// autoAddChannels asks the channel index once a day for each tab that has auto-add on.
// One search message is sent per tab. The signed-in account then opens channels that are not
// already listed and adds one when a recent post looks useful.
func autoAddChannels(ctx context.Context, cat *catalog.Catalog, sources *tgsource.Service, news *newsfeed.Service, users *tguser.Manager, idx *chanindex.Client, log *slog.Logger) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		if !hold.Stopped() {
			work, done := hold.Begin(ctx)
			err := autoAddPass(work, cat, sources, news, users, idx, log)
			cancelled := work.Err() != nil
			done()
			if err != nil && ctx.Err() == nil && !cancelled && log != nil {
				log.Warn("channel auto-add", "err", err)
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

func autoAddPass(ctx context.Context, cat *catalog.Catalog, sources *tgsource.Service, news *newsfeed.Service, users *tguser.Manager, idx *chanindex.Client, log *slog.Logger) error {
	if cat == nil || idx == nil || users == nil {
		return nil
	}
	if sources != nil {
		set, err := sources.Settings(ctx)
		if err != nil {
			return err
		}
		if set.Enabled && set.AutoAdd {
			if err := autoAddTag(ctx, cat, users, idx, log, "direct-file", "chanindex_auto_direct", set, sources, news); err != nil {
				return err
			}
		}
	}
	if news != nil {
		set, err := news.Settings(ctx)
		if err != nil {
			return err
		}
		rules := setRules(set)
		if set.Enabled && set.AutoAdd {
			if err := autoAddTag(ctx, cat, users, idx, log, "news", "chanindex_auto_news", rules, sources, news); err != nil {
				return err
			}
		}
	}
	return nil
}

func setRules(set newsfeed.Settings) tgsource.Settings {
	return tgsource.Settings{
		LookBackDays: set.LookBackDays, EveryMinutes: set.EveryMinutes, MinMinutes: set.MinMinutes,
		MinMB: set.MinMB, MaxGB: set.MaxGB, SkipWords: set.SkipWords, OnlyWords: set.OnlyWords,
		NeedTitle: set.NeedTitle, MaxPerDay: set.MaxPerDay, PauseSeconds: set.PauseSeconds,
	}
}

func autoAddTag(ctx context.Context, cat *catalog.Catalog, users *tguser.Manager, idx *chanindex.Client, log *slog.Logger, tag, key string, rules tgsource.Settings, sources *tgsource.Service, news *newsfeed.Service) error {
	page, ready, err := autoDue(ctx, cat, key, time.Now())
	if err != nil || !ready {
		return err
	}
	got, err := idx.Search(ctx, tag, page)
	if err != nil {
		return err
	}
	listed, err := listedChannels(ctx, cat, tag)
	if err != nil {
		return err
	}
	added := 0
	for _, item := range got.Items {
		if listedChannel(listed, item.Ref) {
			continue
		}
		ok, err := usefulChannel(ctx, users, item.Ref, tag, rules)
		if err != nil {
			if log != nil {
				log.Warn("channel auto-add check", "ref", item.Ref, "err", err)
			}
			continue
		}
		if !ok {
			continue
		}
		if _, err := addDiscoveredChannel(ctx, cat, sources, news, map[string]any{"ref": item.Ref, "tag": tag}); err != nil {
			if log != nil {
				log.Warn("channel auto-add", "ref", item.Ref, "err", err)
			}
			continue
		}
		added++
		listed = append(listed, sourceFromRef(item.Ref))
	}
	next := page + 1
	if got.Pages > 0 && page >= got.Pages {
		next = 1
	}
	if got.Pages == 0 {
		next = 1
	}
	if log != nil {
		log.Info("channel auto-add", "tag", tag, "page", page, "added", added)
	}
	return cat.SetStateValue(ctx, key, fmt.Sprintf("%d %d", time.Now().Unix(), next))
}

func listedChannels(ctx context.Context, cat *catalog.Catalog, tag string) ([]catalog.Source, error) {
	if tag == "news" {
		return cat.NewsChannels(ctx)
	}
	return cat.Sources(ctx)
}

func sourceFromRef(ref string) catalog.Source {
	ref = strings.TrimSpace(ref)
	if name, ok := strings.CutPrefix(ref, "@"); ok {
		return catalog.Source{Username: name}
	}
	if raw, ok := strings.CutPrefix(ref, "c/"); ok {
		id, _ := strconv.ParseInt(raw, 10, 64)
		return catalog.Source{ChannelID: id}
	}
	return catalog.Source{}
}

func listedChannel(list []catalog.Source, ref string) bool {
	want := sourceFromRef(ref)
	for _, s := range list {
		if want.ChannelID != 0 && s.ChannelID == want.ChannelID {
			return true
		}
		if want.Username != "" && strings.EqualFold(s.Username, want.Username) {
			return true
		}
	}
	return false
}

// autoDue reports the page to ask for, and whether a day has passed since the last ask.
func autoDue(ctx context.Context, cat *catalog.Catalog, key string, now time.Time) (int, bool, error) {
	raw, err := cat.StateValue(ctx, key)
	if err != nil {
		return 0, false, err
	}
	page, ready := autoDueRaw(raw, now)
	return page, ready, nil
}

func autoDueRaw(raw string, now time.Time) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 1, true
	}
	var unix int64
	var page int
	if _, err := fmt.Sscanf(raw, "%d %d", &unix, &page); err != nil || page < 1 {
		return 1, true
	}
	if now.Sub(time.Unix(unix, 0)) < 24*time.Hour {
		return page, false
	}
	return page, true
}

func usefulChannel(ctx context.Context, users *tguser.Manager, ref, tag string, rules tgsource.Settings) (bool, error) {
	input, err := channelLookup(ref)
	if err != nil {
		return false, nil
	}
	id, name, err := tguser.ParseChannelRef(input)
	if err != nil {
		return false, nil
	}
	page, err := users.History(ctx, tguser.SourceRef{ChannelID: id, Username: name}, 0, 20)
	if err != nil {
		return false, err
	}
	if tag == "news" {
		return len(page.Links) > 0, nil
	}
	for _, p := range page.Posts {
		if ok, _ := rules.Wanted(p); ok {
			return true, nil
		}
	}
	return false, nil
}
