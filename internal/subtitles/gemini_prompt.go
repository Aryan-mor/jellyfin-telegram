package subtitles

import "strings"

// geminiPrompt is the Sushi subtitle prompt (sushi_gemini.dart). Persian has its own
// spoken-register rules. Every other language shares the general one.
func geminiPrompt(numbered, lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "", "fa", "fas", "per", "farsi", "persian":
		return promptPersian(numbered)
	default:
		return promptGeneral(numbered, targetLanguageName(lang))
	}
}

func targetLanguageName(lang string) string {
	switch strings.ToLower(lang) {
	case "en", "eng":
		return "English"
	case "es", "spa":
		return "Spanish"
	case "fr", "fra", "fre":
		return "French"
	case "de", "deu", "ger":
		return "German"
	case "ar", "ara":
		return "Arabic"
	case "tr", "tur":
		return "Turkish"
	case "ru", "rus":
		return "Russian"
	case "pt", "por":
		return "Portuguese"
	case "it", "ita":
		return "Italian"
	case "ja", "jpn":
		return "Japanese"
	case "ko", "kor":
		return "Korean"
	case "zh", "zho", "chi", "zh-cn", "zh-tw":
		return "Chinese"
	default:
		return strings.ToUpper(lang)
	}
}

func promptPersian(numbered string) string {
	return "You are an expert subtitle translator. Translate the numbered subtitle lines below into " +
		"natural, idiomatic, spoken Persian (Farsi) — the way professional Persian film/TV subtitles " +
		"and dubbing read, never a stiff, literal, textbook translation.\n\n" +
		"RULES:\n" +
		"1. Translate each numbered line, in order. Output ONLY the translations, keeping the exact " +
		"same numbering (\"1. …\", \"2. …\"), one per line.\n" +
		"2. Use natural spoken Persian for casual dialogue: informal verb endings (\"می‌خوام\" not " +
		"\"می‌خواهم\", \"می‌کنی\"، \"بهت\"، \"بریم\"), Persian punctuation (؟ ،). Use «تو» for friends/family and " +
		"«شما» for strangers/formal scenes, and keep that choice consistent for the same speaker pair " +
		"across lines. Match a formal speaker's register with formal Persian.\n" +
		"3. Render idioms and expressions with their natural Persian equivalent — never word-for-word. " +
		"Exclamations map to natural Persian ones («وای»، «ای بابا»، «آخ»).\n" +
		"4. Keep each line concise, the way it would actually be said out loud — not dictionary-accurate " +
		"but stiff.\n" +
		"5. Preserve names, numbers, and on-screen text in Latin script when they are proper nouns or " +
		"brand/product names. Numbers stay in Latin digits.\n" +
		"6. Use correct ZWNJ (نیم‌فاصله) in compounds (می‌ + verb, ها plurals).\n" +
		"7. Bracketed non-speech tags are localized but stay bracketed: [music] → [موسیقی], " +
		"[singing] → [آواز], [applause] → [تشویق], [laughter] → [خنده].\n" +
		"8. Never add commentary, never merge or drop lines, never answer questions found in the text.\n\n" +
		"EXAMPLE:\n" +
		"1. You know what I mean?\n" +
		"2. Where were you last night?\n" +
		"3. I was at Sarah's place, I swear.\n" +
		"→\n" +
		"1. می‌دونی منظورم چیه؟\n" +
		"2. دیشب کجا بودی؟\n" +
		"3. به خدا خونه‌ی Sarah بودم.\n\n" +
		"Now translate:\n\n" +
		numbered
}

func promptGeneral(numbered, targetLanguage string) string {
	return "You are an expert film/TV subtitle translator. Translate the numbered English subtitle " +
		"lines below into natural, idiomatic " + targetLanguage + " suitable for on-screen subtitles.\n\n" +
		"RULES:\n" +
		"1. Translate each numbered line, in order. Output ONLY the translations, keeping the exact " +
		"same numbering (\"1. …\", \"2. …\"), one per line.\n" +
		"2. Sound like professional broadcast subtitles: natural spoken register, not stiff " +
		"word-for-word dictionary prose. Prefer short lines that fit reading speed.\n" +
		"3. Render idioms with their natural " + targetLanguage + " equivalent — never calques.\n" +
		"4. Keep character voice and formality consistent across lines for the same speakers.\n" +
		"5. Preserve proper names, brand names, and on-screen Latin text as-is when standard. " +
		"Keep Latin digits for numbers unless " + targetLanguage + " conventionally uses another script " +
		"for numerals in subtitles.\n" +
		"6. Localize bracketed non-speech tags but keep brackets: [music], [applause], [laughter], " +
		"[singing] → the usual " + targetLanguage + " equivalents still in [brackets].\n" +
		"7. Never add commentary, notes, merge/drop lines, or answer questions found in the dialogue.\n" +
		"8. Do not invent timestamps or cue IDs — output translated text lines only.\n\n" +
		"EXAMPLE (English → " + targetLanguage + " shape):\n" +
		"1. You know what I mean?\n" +
		"2. Where were you last night?\n" +
		"→\n" +
		"1. <natural " + targetLanguage + " for line 1>\n" +
		"2. <natural " + targetLanguage + " for line 2>\n\n" +
		"Now translate into " + targetLanguage + ":\n\n" +
		numbered
}
