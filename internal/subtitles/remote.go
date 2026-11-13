package subtitles

import (
	"context"
	"path"
	"strings"

	"github.com/Aryan-mor/jellyfin-telegram/internal/naming"
)

// RemoteHit is one Telegram document a hashtag search returned.
type RemoteHit struct {
	Name       string
	Caption    string
	Video      bool
	ChannelID  int64
	AccessHash int64
	MessageID  int
	Size       int64
}

// RemoteSource searches Telegram and downloads a small subtitle file.
type RemoteSource interface {
	Search(ctx context.Context, query string) ([]RemoteHit, error)
	Next(ctx context.Context, channelID, accessHash int64, afterID int) (RemoteHit, bool, error)
	Download(ctx context.Context, channelID, accessHash int64, messageID int) (name string, data []byte, err error)
}

// RemoteQuery asks for a subtitle of one title at one quality.
type RemoteQuery struct {
	Episode bool
	TMDB    int
	Season  int
	Number  int
	Lang    string
	Quality string
	Seconds int
}

const maxRemoteTries = 8

// FindRemote returns a subtitle posted under the same hashtag at the same quality.
// A different quality is left for a later source, because the cues would not stay in sync.
func FindRemote(ctx context.Context, src RemoteSource, q RemoteQuery) (*SubFile, error) {
	if src == nil || q.TMDB <= 0 || q.Lang == "" || q.Quality == "" {
		return nil, nil
	}
	base := naming.Tag(q.Episode, q.TMDB, q.Season, q.Number)
	if base == "" {
		return nil, nil
	}
	hits, err := src.Search(ctx, base)
	if err != nil || len(hits) == 0 {
		return nil, err
	}
	tried := 0
	for _, hit := range hits {
		if tried >= maxRemoteTries {
			break
		}
		if !tagHasLang(hit.Name+"\n"+hit.Caption, base, q.Lang) {
			continue
		}
		if !SameQuality(q.Quality, FileQuality(hit.Name, hit.Caption)) {
			continue
		}
		tried++
		file, err := subtitleOf(ctx, src, hit, q)
		if err != nil {
			if ctx.Err() != nil {
				return nil, err
			}
			continue
		}
		if file != nil && fitsRuntime(*file, q.Seconds) {
			return file, nil
		}
	}
	return nil, nil
}

func tagHasLang(text, base, lang string) bool {
	for _, tag := range naming.TagsIn(text) {
		got, _, sub := naming.SplitTag(tag)
		if got == base && sub == lang {
			return true
		}
	}
	return false
}

func subtitleOf(ctx context.Context, src RemoteSource, hit RemoteHit, q RemoteQuery) (*SubFile, error) {
	target := hit
	if hit.Video {
		next, ok, err := src.Next(ctx, hit.ChannelID, hit.AccessHash, hit.MessageID)
		if err != nil || !ok || next.Video {
			return nil, err
		}
		if !subName(next.Name) {
			return nil, nil
		}
		target = next
	} else if !subName(hit.Name) {
		return nil, nil
	}
	name, data, err := src.Download(ctx, target.ChannelID, target.AccessHash, target.MessageID)
	if err != nil || len(data) == 0 {
		return nil, err
	}
	if name == "" {
		name = target.Name
	}
	if strings.EqualFold(path.Ext(name), ".zip") {
		files, err := ExtractSubs(data, q.Lang)
		if err != nil || len(files) == 0 {
			return nil, err
		}
		if q.Episode {
			if pick := pickEpisode(files, q.Season, q.Number); pick != nil {
				return pick, nil
			}
			return nil, nil
		}
		if pick := pickMovie(files, ""); pick != nil {
			return pick, nil
		}
		return &files[0], nil
	}
	file := DecodeFile(name, data, q.Lang)
	if file.Ext != ".srt" && file.Ext != ".ass" {
		return nil, nil
	}
	return &file, nil
}

func subName(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".srt", ".ass", ".ssa", ".vtt", ".sub", ".zip":
		return true
	default:
		return false
	}
}
