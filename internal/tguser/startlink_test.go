package tguser

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gotd/td/tg"
)

func TestStartURLsFromANewsPost(t *testing.T) {
	text := `فیلم تحسین شده "پرایم تایم" منتشر شد
📥 دانلود فیلم: زیرنویس چسبیده (https://t.me/CinamaFansBot?start=87813508) | دوبله پارسی
 (https://t.me/CinamaFansBot?start=683029860)
https://t.me/PoshtSahnehmovie/439`
	got := StartURLs(text, []string{"https://t.me/OtherBot?start=abc"})
	want := []string{
		"https://t.me/cinamafansbot?start=87813508",
		"https://t.me/cinamafansbot?start=683029860",
		"https://t.me/otherbot?start=abc",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
}

func TestStartURLsAllowSlashBeforeQuery(t *testing.T) {
	text := `تریلر (https://t.me/NeonToonsBot/?start=DL_f7bdb2e9f0)
فصل 1 1080p (https://t.me/NeonToonsBot/?start=DL_a3d7898eaf) - 720p (https://t.me/NeonToonsBot/?start=DL_fdb371a4e5) - 480p (https://t.me/NeonToonsBot/?start=DL_b63fa85812)
فصل 2 720p (https://t.me/NeonToonsBot/?start=DL_a09beb714a)`
	got := StartURLs(text, nil)
	want := []string{
		"https://t.me/neontoonsbot?start=DL_f7bdb2e9f0",
		"https://t.me/neontoonsbot?start=DL_a3d7898eaf",
		"https://t.me/neontoonsbot?start=DL_fdb371a4e5",
		"https://t.me/neontoonsbot?start=DL_b63fa85812",
		"https://t.me/neontoonsbot?start=DL_a09beb714a",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
}

func TestStartURLsAcceptTheFormsPeoplePaste(t *testing.T) {
	text := strings.Join([]string{
		"https://t.me/SomeBot?start=a",
		"http://t.me/SomeBot/?start=b",
		"https://www.t.me/SomeBot?start=c",
		"t.me/SomeBot?start=d",
		"www.t.me/SomeBot/?start=e",
		"https://telegram.me/SomeBot?start=f",
		"telegram.me/SomeBot/?start=g",
		"http://www.telegram.me/SomeBot?start=h",
		"https://telegram.dog/SomeBot?start=i",
		"//t.me/SomeBot?start=j",
		"tg://resolve?domain=SomeBot&start=k",
		"https://t.me/SomeBot?foo=1&start=m",
		"https://example.com/t.me/SomeBot?start=no",
		"https://t.me/channelonly",
		"https://t.me/SomeBot?start=",
	}, " ")
	got := StartURLs(text, []string{"http://www.telegram.me/Hidden/?start=n"})
	want := []string{
		"https://t.me/somebot?start=a",
		"https://t.me/somebot?start=b",
		"https://t.me/somebot?start=c",
		"https://t.me/somebot?start=d",
		"https://t.me/somebot?start=e",
		"https://t.me/somebot?start=f",
		"https://t.me/somebot?start=g",
		"https://t.me/somebot?start=h",
		"https://t.me/somebot?start=i",
		"https://t.me/somebot?start=j",
		"https://t.me/somebot?start=k",
		"https://t.me/somebot?start=m",
		"https://t.me/hidden?start=n",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
}

func TestButtonURLsAreStartLinks(t *testing.T) {
	msg := &tg.Message{
		Message: "تریلر",
		ReplyMarkup: &tg.ReplyInlineMarkup{Rows: []tg.KeyboardInlineButtonRow{{
			Buttons: []tg.KeyboardInlineButton{
				{Text: "1080p", Type: &tg.InlineButtonTypeURL{URL: "https://t.me/NeonToonsBot/?start=DL_a3d7898eaf"}},
				{Text: "720p", Type: &tg.InlineButtonTypeURL{URL: "t.me/NeonToonsBot?start=DL_fdb371a4e5"}},
				{Text: "channel", Type: &tg.InlineButtonTypeURL{URL: "https://t.me/NeonToonsORG"}},
			},
		}}},
	}
	got := StartURLs(msg.Message, append(textURL(msg), buttonURLs(msg)...))
	want := []string{
		"https://t.me/neontoonsbot?start=DL_a3d7898eaf",
		"https://t.me/neontoonsbot?start=DL_fdb371a4e5",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
}

func TestStartURLsIgnorePlainChannelLinks(t *testing.T) {
	if got := StartURLs("https://t.me/CinamaFans https://t.me/bot?start=", nil); len(got) != 0 {
		t.Fatalf("got %#v", got)
	}
}
