package lang

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := map[string]string{
		"fa": "fa", "per": "fa", "fas": "fa", " FAS ": "fa", "pes": "fa", "arb": "ar",
		"de": "de", "ger": "de", "deu": "de",
		"fr": "fr", "fre": "fr", "fra": "fr",
		"zh": "zh", "chi": "zh", "zho": "zh", "cmn": "zh",
		"en": "en", "eng": "en", "pt-BR": "pt", "spa": "es",
		"": "", "und": "", "mul": "", "zxx": "", "xxx": "", "not a code": "",
	}
	for in, want := range tests {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestISO3(t *testing.T) {
	for in, want := range map[string]string{"fa": "fas", "de": "deu", "en": "eng", "ja": "jpn"} {
		if got := ISO3(in); got != want {
			t.Errorf("ISO3(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInTitle(t *testing.T) {
	tests := []struct {
		code, title string
		want        bool
	}{
		{"fa", "Persian SDH", true},
		{"fa", "زیرنویس فارسی", true},
		{"fa", "Farsi", true},
		{"de", "German (Forced)", true},
		{"de", "Deutsch", true},
		{"fr", "Français", true},
		{"en", "English", true},
		{"de", "French", false},
		{"fa", "English", false},
		{"", "English", false},
		{"en", "", false},
	}
	for _, tc := range tests {
		if got := InTitle(tc.code, tc.title); got != tc.want {
			t.Errorf("InTitle(%q, %q) = %v", tc.code, tc.title, got)
		}
	}
}

func TestTitleInScript(t *testing.T) {
	tests := []struct {
		code, title string
		want        bool
	}{
		{"fa", "جدایی نادر از سیمین", true},
		{"ru", "Сталкер", true},
		{"ko", "기생충", true},
		{"fa", "Heat", false},
		{"ru", "جدایی", false},
		// Latin script does not identify a language.
		{"de", "Der Untergang", false},
		{"en", "Heat", false},
	}
	for _, tc := range tests {
		if got := TitleInScript(tc.code, tc.title); got != tc.want {
			t.Errorf("TitleInScript(%q, %q) = %v", tc.code, tc.title, got)
		}
	}
}

func cues(lines ...string) string {
	var b strings.Builder
	for i, l := range lines {
		b.WriteString("1\n00:00:01,000 --> 00:00:02,000\n<i>")
		b.WriteString(l)
		b.WriteString("</i>\n\n")
		_ = i
	}
	return b.String()
}

func TestIsLanguage(t *testing.T) {
	samples := map[string]string{
		"fa": cues("من امروز صبح زود از خواب بیدار شدم و به مدرسه رفتم", "چرا این کار را انجام می‌دهی؟ من نمی‌دانم چه باید بگویم", "ما باید همین حالا از اینجا برویم چون خیلی دیر شده است"),
		"ar": cues("لقد استيقظت مبكرا هذا الصباح وذهبت إلى المدرسة مع أصدقائي", "لماذا تفعل هذا؟ لا أعرف ماذا يجب أن أقول لك الآن", "يجب علينا أن نغادر هذا المكان الآن لأن الوقت قد تأخر"),
		"en": cues("I woke up early this morning and went to school with my friends", "Why are you doing this? I do not know what I should say to you", "We have to leave this place right now because it is getting late"),
		"de": cues("Ich bin heute Morgen früh aufgestanden und mit meinen Freunden zur Schule gegangen", "Warum machst du das? Ich weiß nicht, was ich dir sagen soll", "Wir müssen jetzt sofort von hier weggehen, weil es schon sehr spät ist"),
		"fr": cues("Je me suis levé tôt ce matin et je suis allé à l'école avec mes amis", "Pourquoi fais-tu cela? Je ne sais pas ce que je dois te dire maintenant", "Nous devons quitter cet endroit tout de suite parce qu'il est très tard"),
		"ru": cues("Сегодня утром я проснулся рано и пошёл в школу со своими друзьями", "Почему ты это делаешь? Я не знаю, что тебе сказать сейчас", "Нам нужно уйти отсюда прямо сейчас, потому что уже очень поздно"),
	}
	for code, text := range samples {
		if !IsLanguage(code, text) {
			t.Errorf("%s text was not recognised", code)
		}
		for other := range samples {
			if other != code && IsLanguage(other, text) {
				t.Errorf("%s text was taken for %s", code, other)
			}
		}
	}
	if IsLanguage("en", cues("Hi")) {
		t.Error("a very short sample should count as no")
	}
}

func TestIsLanguageOnAWholeSubtitleFile(t *testing.T) {
	long := map[string][]string{
		"fa": {"من امروز صبح زود از خواب بیدار شدم و به مدرسه رفتم", "چرا این کار را انجام می‌دهی؟ من نمی‌دانم چه باید بگویم", "ما باید همین حالا از اینجا برویم چون خیلی دیر شده است", "پدرم گفت که فردا با هم به خانه مادربزرگ خواهیم رفت"},
		"en": {"I woke up early this morning and went to school", "Why are you doing this? I do not know what to say", "We have to leave this place right now, it is getting late", "My father said that we would visit our grandmother tomorrow"},
		"de": {"Ich bin heute Morgen früh aufgestanden und zur Schule gegangen", "Warum machst du das? Ich weiß nicht, was ich sagen soll", "Wir müssen jetzt sofort von hier weggehen, es ist schon spät", "Mein Vater sagte, dass wir morgen unsere Großmutter besuchen werden"},
		"ru": {"Сегодня утром я проснулся рано и пошёл в школу", "Почему ты это делаешь? Я не знаю, что сказать", "Нам нужно уйти отсюда прямо сейчас, уже очень поздно", "Мой отец сказал, что завтра мы поедем к бабушке"},
	}
	for code, variety := range long {
		lines := make([]string, 3000)
		for i := range lines {
			lines[i] = variety[i%len(variety)]
		}
		if !IsLanguage(code, cues(lines...)) {
			t.Errorf("%s: a long file was not recognised", code)
		}
	}
}

func TestLegacyDecoderRoundTrips(t *testing.T) {
	tests := map[string]string{
		"fa": "سلام دنيا", "ru": "Привет", "el": "Γειά", "de": "Grüße", "tr": "Teşekkür",
		"he": "שלום", "pl": "Dziękuję", "ja": "こんにちは", "ko": "안녕", "zh": "你好",
	}
	for code, text := range tests {
		enc := LegacyDecoder(code)
		raw, err := enc.NewEncoder().String(text)
		if err != nil {
			t.Errorf("%s: encode: %v", code, err)
			continue
		}
		back, err := enc.NewDecoder().String(raw)
		if err != nil || back != text {
			t.Errorf("%s: got %q, %v", code, back, err)
		}
	}
}
