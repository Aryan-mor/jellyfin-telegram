package tgsetup

import (
	"context"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

// recorder is a Telegram connection that remembers the one request it was given.
type recorder struct {
	got *tg.AccountUpdateNotifySettingsRequest
}

func (r *recorder) Invoke(_ context.Context, in bin.Encoder, out bin.Decoder) error {
	var b bin.Buffer
	if err := in.Encode(&b); err != nil {
		return err
	}
	req := &tg.AccountUpdateNotifySettingsRequest{}
	if err := req.Decode(&b); err != nil {
		return err
	}
	r.got = req
	var reply bin.Buffer
	if err := (&tg.BoolTrue{}).Encode(&reply); err != nil {
		return err
	}
	return out.Decode(&reply)
}

func TestMuteChannelMutesThatChannelForGood(t *testing.T) {
	rec := &recorder{}
	if err := MuteChannel(context.Background(), tg.NewClient(rec), 1234567, 99); err != nil {
		t.Fatal(err)
	}
	if rec.got == nil {
		t.Fatal("no request was sent")
	}
	peer, ok := rec.got.Peer.(*tg.InputNotifyPeer)
	if !ok {
		t.Fatalf("peer = %T", rec.got.Peer)
	}
	ch, ok := peer.Peer.(*tg.InputPeerChannel)
	if !ok || ch.ChannelID != 1234567 || ch.AccessHash != 99 {
		t.Fatalf("muted %+v, want channel 1234567 with hash 99", peer.Peer)
	}
	if rec.got.Settings.MuteUntil != 2147483647 {
		t.Fatalf("MuteUntil = %d, want the no-end value", rec.got.Settings.MuteUntil)
	}
}
