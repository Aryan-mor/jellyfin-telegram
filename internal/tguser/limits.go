// Package tguser keeps Telegram user sessions that post files into the channel.
// Bots stay in tgpool. A user session is what can upload a video the channel will then play.
package tguser

// PartSize is the upload part size. Telegram accepts at most 512 KiB, and the part
// count is what caps the file: 4000 parts for a normal account, 8000 for Premium.
const PartSize = 512 * 1024

const (
	regularParts = 4000
	premiumParts = 8000
)

// MaxBytes is the largest file this account can send as one document.
func MaxBytes(premium bool) int64 {
	parts := int64(regularParts)
	if premium {
		parts = premiumParts
	}
	return parts * PartSize
}
