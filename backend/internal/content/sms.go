package content

import "unicode/utf8"

// gsm7 is the GSM 03.38 basic character set plus its extension table.
// Anything outside it (including Turkish ş, ğ, ı, İ, ç) forces UCS-2, which
// shrinks a single SMS segment from 160 to 70 characters.
const gsm7 = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà" +
	"^{}\\[~]|€"

// SMSInfo describes how a rendered SMS would be billed by a carrier.
type SMSInfo struct {
	Characters int    `json:"characters"`
	Encoding   string `json:"encoding"` // GSM-7 | UCS-2
	Segments   int    `json:"segments"`
}

// AnalyzeSMS reports encoding and segment count. The product limit is 160
// characters (validated separately); this is shown to the marketer as cost
// guidance because Turkish text usually encodes as UCS-2.
func AnalyzeSMS(text string) SMSInfo {
	n := utf8.RuneCountInString(text)
	enc, single, multi := "GSM-7", 160, 153
	for _, r := range text {
		if !isGSM7(r) {
			enc, single, multi = "UCS-2", 70, 67
			break
		}
	}
	segments := 1
	if n > single {
		segments = (n + multi - 1) / multi
	}
	return SMSInfo{Characters: n, Encoding: enc, Segments: segments}
}

func isGSM7(r rune) bool {
	for _, g := range gsm7 {
		if r == g {
			return true
		}
	}
	return false
}
