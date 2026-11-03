package subarchive

import (
	"context"
	"path"
	"strings"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/lang"
	"github.com/Aryan-mor/jellyfin-telegram/internal/media"
	"github.com/Aryan-mor/jellyfin-telegram/internal/naming"
	"github.com/Aryan-mor/jellyfin-telegram/internal/subtitles"
)

// ChannelSubs finds subtitle files the user posted to the channel. A file belongs to a video when
// it replies to that video, or when its name parses to the same title and episode.
type ChannelSubs struct {
	Catalog  *catalog.Catalog
	Channel  Channel
	Language string
}

// Lookup returns a subtitle for a video in the configured language, or nil.
func (c *ChannelSubs) Lookup(ctx context.Context, m subtitles.Meta) (*subtitles.SubFile, error) {
	all, err := c.Catalog.Attachments(ctx)
	if err != nil {
		return nil, err
	}
	var video *media.File
	if f, err := c.Catalog.Get(ctx, m.MessageID); err == nil {
		video = &f
	}
	for _, a := range all {
		if IsArchiveName(a.FileName) || a.DocID == 0 || !belongs(a, m.MessageID, video) {
			continue
		}
		data, err := c.Channel.Download(ctx, a.MessageID, a.DocID, a.Size)
		if err != nil {
			return nil, err
		}
		for _, f := range c.files(a, data) {
			if lang.IsLanguage(c.Language, f.Text) {
				picked := f
				return &picked, nil
			}
		}
	}
	return nil, nil
}

func (c *ChannelSubs) files(a media.Attachment, data []byte) []subtitles.SubFile {
	if strings.EqualFold(path.Ext(a.FileName), ".zip") {
		files, err := subtitles.ExtractSubs(data, c.Language)
		if err != nil {
			return nil
		}
		return files
	}
	return []subtitles.SubFile{subtitles.DecodeFile(a.FileName, data, c.Language)}
}

func belongs(a media.Attachment, messageID int, video *media.File) bool {
	if a.ReplyTo != 0 {
		return a.ReplyTo == messageID
	}
	if video == nil {
		return false
	}
	want := naming.Parse(video.FileName, video.Caption)
	got := naming.Parse(a.FileName, "")
	return want.Title != "" && strings.EqualFold(want.Title, got.Title) &&
		want.Episode == got.Episode && want.Season == got.Season && want.Number == got.Number
}
