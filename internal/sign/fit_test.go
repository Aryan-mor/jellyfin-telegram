package sign

import (
	"strings"
	"testing"
)

func TestFitKeepsTitleLineAndTag(t *testing.T) {
	line := LinePrefix + ";k=m;t=1;h=" + strings.Repeat("a", 22) + ";s=" + strings.Repeat("b", 86)
	caption := StatusLine(StatusReady, "") + "\n" + ClaimPrefix + ";" + strings.Repeat("c", 200) + "\nTitle (2020)\n1080p\n🔊 en\n💬 " + strings.Repeat("fa, ", 400) + "\nsource\n" + line + "\n#tm_m_1"
	out := Fit(caption)
	if CaptionLen(out) > CaptionLimit {
		t.Fatalf("still %d long", CaptionLen(out))
	}
	for _, want := range []string{"Title (2020)", line, "#tm_m_1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("lost %q in %q", want, out)
		}
	}
	if strings.Contains(out, ClaimPrefix) || strings.Contains(out, "💬") {
		t.Fatalf("lesser lines stayed: %q", out)
	}
}

func TestFitKeepsLesserLinesWhenTheyFit(t *testing.T) {
	caption := "Title\n1080p\n🔊 en\njftg:2;k=m\n#tm_m_1"
	if got := Fit(caption); got != caption {
		t.Fatalf("changed a short caption: %q", got)
	}
}

func TestFitShortensAHugeTitleLast(t *testing.T) {
	line := LinePrefix + ";k=m;t=1;s=" + strings.Repeat("b", 86)
	caption := strings.Repeat("Я", 2000) + "\n" + line + "\n#tm_m_1"
	out := Fit(caption)
	if CaptionLen(out) > CaptionLimit || !strings.Contains(out, line) || !strings.HasSuffix(out, "#tm_m_1") {
		t.Fatalf("bad fit: len %d", CaptionLen(out))
	}
}

func TestCaptionLenCountsUTF16(t *testing.T) {
	if CaptionLen("a💬") != 3 || CaptionLen("Яб") != 2 {
		t.Fatal("wrong length")
	}
}
