package tguser

import "testing"

func TestTitleReadyMatchesAnyLanguage(t *testing.T) {
	same := []string{
		"A\n#tm_m_603",
		"A\n#tm_m_603_en",
		"A\n#tm_m_603_en_fa",
		"A\n#tm_m_603_none_fa",
	}
	for _, caption := range same {
		if !captionsHold("#tm_m_603", []string{caption}) || !captionsHold("#tm_m_603_fa", []string{caption}) {
			t.Fatalf("wanted the film in %q", caption)
		}
	}
	if captionsHold("#tm_m_603", []string{"A\n#tm_m_6030"}) || captionsHold("#tm_m_603_en", []string{"A\n#tm_m_6030_en"}) {
		t.Fatal("a longer film id counted")
	}
	if !captionsHold("#tm_tv_231751_1_6", []string{"A\n#tm_tv_231751_1_6_en_fa"}) {
		t.Fatal("episode language version")
	}
	if !captionsHold("#tm_tv_231751_1_6_en_fa", []string{"A\n#tm_tv_231751_1_6"}) {
		t.Fatal("bare episode")
	}
	if captionsHold("#tm_tv_2317", []string{"A\n#tm_tv_231751_1_6_en_fa"}) {
		t.Fatal("a shorter series id counted")
	}
	if captionsHold("#tm_tv_231751_1_6", []string{"A\n#tm_tv_231751_1_7_en"}) {
		t.Fatal("a different episode counted")
	}
}
