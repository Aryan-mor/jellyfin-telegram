package tgsetup

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func TestChannelUsernameShape(t *testing.T) {
	re := regexp.MustCompile(`^[a-z][a-z0-9]*_[a-z0-9]{6}$`)
	for _, title := range []string{"Films", "ختم", "123 films", "A very long channel title indeed"} {
		got := channelUsername(title)
		if !re.MatchString(got) {
			t.Errorf("%q gave %q", title, got)
		}
	}
	if a, b := channelUsername("Films"), channelUsername("Films"); a == b {
		t.Error("two usernames were equal")
	}
}

// usernameScript answers check and update calls. The first check is reported as taken.
type usernameScript struct {
	checks  []string
	updates []string
	seen    int
}

func (s *usernameScript) Invoke(_ context.Context, in bin.Encoder, out bin.Decoder) error {
	var buf bin.Buffer
	if err := in.Encode(&buf); err != nil {
		return err
	}
	raw := append([]byte(nil), buf.Buf...)
	check := &tg.ChannelsCheckUsernameRequest{}
	if err := check.Decode(&bin.Buffer{Buf: append([]byte(nil), raw...)}); err == nil && check.Username != "" {
		s.checks = append(s.checks, check.Username)
		s.seen++
		ok := s.seen > 1
		return writeBool(out, ok)
	}
	upd := &tg.ChannelsUpdateUsernameRequest{}
	if err := upd.Decode(&bin.Buffer{Buf: raw}); err == nil {
		s.updates = append(s.updates, upd.Username)
		return writeBool(out, true)
	}
	return writeBool(out, false)
}

func writeBool(out bin.Decoder, ok bool) error {
	var reply bin.Buffer
	var v bin.Encoder = &tg.BoolFalse{}
	if ok {
		v = &tg.BoolTrue{}
	}
	if err := v.Encode(&reply); err != nil {
		return err
	}
	return out.Decode(&reply)
}

func TestSetPublicUsernameRetriesWhenTaken(t *testing.T) {
	script := &usernameScript{}
	name, err := SetPublicUsername(context.Background(), tg.NewClient(script), 7, 9, "", "Films")
	if err != nil {
		t.Fatal(err)
	}
	if len(script.checks) < 2 {
		t.Fatalf("checks = %v, want a retry", script.checks)
	}
	if script.checks[0] == script.checks[1] {
		t.Fatalf("retry reused %q", script.checks[0])
	}
	if name != script.checks[1] || len(script.updates) != 1 || script.updates[0] != name {
		t.Fatalf("name %q checks %v updates %v", name, script.checks, script.updates)
	}
}

func TestClearUsernameSendsAnEmptyName(t *testing.T) {
	script := &usernameScript{}
	if err := ClearUsername(context.Background(), tg.NewClient(script), 7, 9); err != nil {
		t.Fatal(err)
	}
	if len(script.updates) != 1 || script.updates[0] != "" {
		t.Fatalf("updates = %v", script.updates)
	}
}

func TestValidChannelUsername(t *testing.T) {
	for name, want := range map[string]bool{
		"films_ab12cd": true, "abcde": true, "abcd": false, "1films": false, "_films": false,
		"films_": false, "fil__ms": false, "films-hd": false, "a234567890123456789012345678901b": true,
		"a2345678901234567890123456789012c": false,
	} {
		if got := ValidChannelUsername(name); got != want {
			t.Errorf("%q = %v, want %v", name, got, want)
		}
	}
	for range 50 {
		if name := channelUsername("Films"); !ValidChannelUsername(name) {
			t.Fatalf("random name %q is not valid", name)
		}
	}
}

func TestUsernameErrorIsReadable(t *testing.T) {
	if err := usernameError("x", "set", errors.New("rpc error code 400: USERNAME_OCCUPIED")); !errors.Is(err, errUsernameTaken) {
		t.Errorf("occupied = %v", err)
	}
	if err := usernameError("x", "set", errors.New("rpc error code 400: CHANNELS_ADMIN_PUBLIC_TOO_MUCH")); !errors.Is(err, ErrPublicLimit) {
		t.Errorf("limit = %v", err)
	}
	if err := usernameError("x", "set", errors.New("rpc error code 400: USERNAME_INVALID")); errors.Is(err, errUsernameTaken) || !strings.Contains(err.Error(), "not a valid username") {
		t.Errorf("invalid = %v", err)
	}
}

func TestSetPublicUsernameRefusesABadName(t *testing.T) {
	script := &usernameScript{}
	if _, err := SetPublicUsername(context.Background(), tg.NewClient(script), 7, 9, "-bad", "Films"); err == nil {
		t.Fatal("a bad name was accepted")
	}
	if len(script.checks) != 0 {
		t.Fatalf("Telegram was asked about a bad name: %v", script.checks)
	}
}
