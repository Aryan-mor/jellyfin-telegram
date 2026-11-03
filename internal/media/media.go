// Package media holds the types shared across the service.
package media

import "time"

// BlockSize is the unit of every Telegram read and of the disk cache.
const BlockSize int64 = 1 << 20

// File is one video document in the channel, addressed by its message id.
type File struct {
	MessageID int
	ChannelID int64
	DocID     int64
	Size      int64
	MIME      string
	FileName  string
	Caption   string
	Date      time.Time
	HasThumb  bool

	// RelPath is the .strm location inside the library, using forward slashes.
	RelPath string
	// ProbedAt is zero until Jellyfin has probed this file.
	ProbedAt time.Time
	// DurationSeconds is zero until a probe has reported it.
	DurationSeconds int
	// SubtitleOutcome is empty until the subtitle check has finished.
	SubtitleOutcome string
	// ExtractState is empty until the embedded subtitles have been handled.
	ExtractState string
}

// Values of File.ExtractState.
const (
	ExtractNone = "none" // the file has no embedded subtitles to keep
	ExtractDone = "done" // the subtitles are in the channel and in Jellyfin's cache
)

// Attachment is a subtitle file or archive in the channel.
type Attachment struct {
	MessageID int
	DocID     int64
	Size      int64
	FileName  string
	// ReplyTo is the message this one replies to, or zero.
	ReplyTo int
}

// Message is the result of looking up one id in the channel.
type Message struct {
	ID int
	// Exists is false when the id was never used or the message was deleted.
	Exists bool
	// File is nil when the message exists but holds no video.
	File *File
	// Attachment is set when the message holds a subtitle file.
	Attachment *Attachment
}
