package tgsource

import (
	"path"
	"strconv"
	"strings"

	"github.com/Aryan-mor/jellyfin-telegram/internal/naming"
	"github.com/Aryan-mor/jellyfin-telegram/internal/sign"
	"github.com/Aryan-mor/jellyfin-telegram/internal/subarchive"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// subExt are loose subtitle files someone posted under a video, not an archive this service built.
var subExt = map[string]bool{
	".srt": true, ".ass": true, ".ssa": true, ".vtt": true, ".sub": true,
}

// edgeMsg is the oldest message of the page just newer than the one being read.
// In the channel that message sits directly below the newest message of the older page.
type edgeMsg struct {
	id   int
	post tguser.Post
	doc  bool
}

// SubtitleFor reports that next is the subtitle of video. next must already be the message
// directly below video. A following video, or a file for a different document, is not.
func SubtitleFor(video, next tguser.Post) bool {
	if next.ID == 0 || next.ID == video.ID || next.DocID == 0 || video.DocID == 0 {
		return false
	}
	if _, docID, ok := subarchive.ParseName(next.Name); ok && docID == video.DocID {
		return true
	}
	if subs, ok := sign.ParseSubsCaption(next.Caption); ok && subs.Authentic() && subs.DocID == video.DocID {
		return true
	}
	if !subExt[strings.ToLower(path.Ext(next.Name))] {
		return false
	}
	if next.ReplyTo == video.ID {
		return true
	}
	want := naming.Parse(video.Name, video.Caption)
	got := naming.Parse(next.Name, "")
	return want.Title != "" && strings.EqualFold(want.Title, got.Title) &&
		want.Episode == got.Episode && want.Season == got.Season && want.Number == got.Number
}

// subsBelow maps a video message id to the subtitle document posted immediately under it.
func subsBelow(page tguser.Page, edge edgeMsg) map[int]tguser.Post {
	out := map[int]tguser.Post{}
	for _, p := range page.Posts {
		next, ok := documentBelow(p.ID, page, edge)
		if !ok || !SubtitleFor(p, next) {
			continue
		}
		out[p.ID] = next
	}
	return out
}

// documentBelow is the next message when that message is a document.
// A text post or a service message in between hides whatever follows it.
func documentBelow(id int, page tguser.Page, edge edgeMsg) (tguser.Post, bool) {
	ids := page.IDs
	if len(ids) == 0 {
		for _, p := range page.Posts {
			ids = append(ids, p.ID)
		}
	}
	if edge.id != 0 {
		ids = append(ids, edge.id)
	}
	next := 0
	for _, n := range ids {
		if n > id && (next == 0 || n < next) {
			next = n
		}
	}
	if next == 0 {
		return tguser.Post{}, false
	}
	if edge.doc && edge.id == next {
		return edge.post, true
	}
	for _, p := range page.Posts {
		if p.ID == next {
			return p, true
		}
	}
	return tguser.Post{}, false
}

func oldestEdge(page tguser.Page) edgeMsg {
	if page.Lowest == 0 {
		return edgeMsg{}
	}
	edge := edgeMsg{id: page.Lowest}
	for _, p := range page.Posts {
		if p.ID == page.Lowest {
			edge.post = p
			edge.doc = true
			break
		}
	}
	return edge
}

// archiveName is the file name this service gives a subtitle zip, kept here for tests that
// should not depend on a particular message id in the name.
func archiveName(docID int64) string {
	return "jftg-subs-1-" + strconv.FormatInt(docID, 10) + ".zip"
}
