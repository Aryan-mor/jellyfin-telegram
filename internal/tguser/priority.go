package tguser

import "context"

type userJobKey struct{}

// Prioritise marks a job the person asked for, such as creating a mirror or the main
// channel. The account queue and the bot queue run it before inbox, search, and forwards.
// A FLOOD_WAIT still holds it. Automatic work must not use this.
func Prioritise(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, userJobKey{}, true)
}

// UserJob reports whether ctx was marked with Prioritise.
func UserJob(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(userJobKey{}).(bool)
	return v
}
