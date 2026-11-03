package tguser

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

type scripted struct {
	hits int
}

func (s *scripted) Invoke(_ context.Context, input bin.Encoder, output bin.Decoder) error {
	s.hits++
	if s.hits == 1 {
		return tgerr.New(420, "FLOOD_WAIT_0")
	}
	return nil
}

func TestFloodWaitRetriesUploadSave(t *testing.T) {
	var waited int
	next := floodWait(func() { waited++ })
	inv := next.Handle(&scripted{})
	err := inv.Invoke(context.Background(), &tg.UploadSaveFilePartRequest{}, discard{})
	if err != nil || waited != 1 {
		t.Fatalf("err=%v waited=%d", err, waited)
	}
}

func TestFloodWaitDoesNotHideOtherMethods(t *testing.T) {
	next := floodWait(func() {})
	inv := next.Handle(invokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		return tgerr.New(420, "FLOOD_WAIT_1")
	}))
	err := inv.Invoke(context.Background(), &tg.MessagesGetDialogsRequest{}, discard{})
	if err == nil {
		t.Fatal("expected the flood error to pass through")
	}
}

type discard struct{}

func (discard) Decode(*bin.Buffer) error { return nil }

type invokeFunc func(context.Context, bin.Encoder, bin.Decoder) error

func (f invokeFunc) Invoke(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
	return f(ctx, in, out)
}

func TestFatalAuth(t *testing.T) {
	if !fatalAuth(errors.New("rpc: SESSION_REVOKED")) || fatalAuth(errors.New("timeout")) {
		t.Fatal("classification")
	}
}

func TestStoreRoundTrip(t *testing.T) {
	path := t.TempDir() + "/7.json"
	s := &fileStore{path: path}
	if err := s.StoreSession(context.Background(), []byte("session-bytes")); err != nil {
		t.Fatal(err)
	}
	meta := Meta{UserID: 7, Admin: true, Premium: true}
	if err := s.writeMeta(meta); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadSession(context.Background())
	if err != nil || string(got) != "session-bytes" {
		t.Fatalf("%q %v", got, err)
	}
	rec, err := s.read()
	if err != nil || !rec.Meta.Admin || rec.Meta.UserID != 7 {
		t.Fatalf("%+v %v", rec.Meta, err)
	}
	_ = time.Second
}
