package autofilter

import (
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

// Option is one value a person can pick, with its English name.
type Option struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// isoCountries are the ISO 3166-1 alpha-2 codes in use.
const isoCountries = "AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS " +
	"BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET " +
	"FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO " +
	"IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH " +
	"MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM " +
	"PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG " +
	"TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW"

// isoLanguages are the ISO 639-1 codes in use.
const isoLanguages = "aa ab ae af ak am an ar as av ay az ba be bg bi bm bn bo br bs ca ce ch co cr cs cu cv cy da de " +
	"dv dz ee el en eo es et eu fa ff fi fj fo fr fy ga gd gl gn gu gv ha he hi ho hr ht hu hy hz ia id ie ig ii ik " +
	"io is it iu ja jv ka kg ki kj kk kl km kn ko kr ks ku kv kw ky la lb lg li ln lo lt lu lv mg mh mi mk ml mn mr " +
	"ms mt my na nb nd ne ng nl nn no nr nv ny oc oj om or os pa pi pl ps pt qu rm rn ro ru rw sa sc sd se sg si sk " +
	"sl sm sn so sq sr ss st su sv sw ta te tg th ti tk tl tn to tr ts tt tw ty ug uk ur uz ve vi vo wa wo xh yi yo " +
	"za zh zu"

// Countries lists the ISO 3166-1 countries with English names.
func Countries() []Option {
	var out []Option
	for _, code := range strings.Fields(isoCountries) {
		name := code
		if r, err := language.ParseRegion(code); err == nil {
			if n := display.English.Regions().Name(r); n != "" {
				name = n
			}
		}
		out = append(out, Option{ID: code, Name: name})
	}
	return out
}

// Languages lists the ISO 639-1 languages with English names.
func Languages() []Option {
	var out []Option
	for _, code := range strings.Fields(isoLanguages) {
		name := code
		if b, err := language.ParseBase(code); err == nil {
			if n := display.English.Languages().Name(b); n != "" {
				name = n
			}
		}
		out = append(out, Option{ID: code, Name: name})
	}
	return out
}

// NetworkOptions lists the streaming brands.
func NetworkOptions() []Option {
	out := make([]Option, 0, len(Networks))
	for _, n := range Networks {
		out = append(out, Option{ID: n.ID, Name: n.Name})
	}
	return out
}

// GenreOptions turns genre names into options. The id is the name, as the filter stores names.
func GenreOptions(names []string) []Option {
	out := make([]Option, 0, len(names))
	for _, n := range names {
		out = append(out, Option{ID: n, Name: n})
	}
	return out
}
