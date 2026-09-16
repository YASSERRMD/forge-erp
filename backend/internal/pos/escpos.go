package pos

import (
	"strings"
)

// ESC/POS byte builders for the till printer (Phase 2 TakePOS depth).
//
// No hardware is ever touched: these are pure byte builders whose output the
// client downloads (GET /pos/sales/{id}/escpos) and forwards to its own
// printer spooler. Command set is the de-facto ESC/POS subset:
//   - ESC @            initialize
//   - ESC p m t1 t2     open cash drawer (m=0, pulse 25ms/250ms)
//   - GS V m            cut paper (m=0 full, m=1 partial)
// Receipt text is encoded to printer-safe 7-bit ASCII (transliterating common
// Latin-1; anything else becomes '?') because cheap thermal printers only
// implement code pages reliably for ASCII.

// InitBytes returns ESC @ (reset printer state before a job).
func InitBytes() []byte { return []byte{0x1B, 0x40} }

// DrawerKickBytes returns ESC p 0 25 250 (open cash drawer, connector pin 2).
func DrawerKickBytes() []byte { return []byte{0x1B, 0x70, 0x00, 0x19, 0xFA} }

// CutBytes returns GS V m (full cut m=0, partial/tear-bar m=1).
func CutBytes(full bool) []byte {
	if full {
		return []byte{0x1D, 0x56, 0x00}
	}
	return []byte{0x1D, 0x56, 0x01}
}

// latin1Fold maps common Latin-1 runes to ASCII for thermal printers without
// a reliable code page. Unlisted non-ASCII becomes '?'.
func latin1Fold(r rune) byte {
	if r < 0x80 {
		return byte(r)
	}
	switch r {
	case 'à', 'á', 'â', 'ã', 'ä', 'å', 'À', 'Á', 'Â', 'Ã', 'Ä', 'Å':
		return 'A'
	case 'è', 'é', 'ê', 'ë', 'È', 'É', 'Ê', 'Ë':
		return 'E'
	case 'ì', 'í', 'î', 'ï', 'Ì', 'Í', 'Î', 'Ï':
		return 'I'
	case 'ò', 'ó', 'ô', 'õ', 'ö', 'Ò', 'Ó', 'Ô', 'Õ', 'Ö':
		return 'O'
	case 'ù', 'ú', 'û', 'ü', 'Ù', 'Ú', 'Û', 'Ü':
		return 'U'
	case 'ç', 'Ç':
		return 'C'
	case 'ñ', 'Ñ':
		return 'N'
	case 'ý', 'ÿ', 'Ý':
		return 'Y'
	case 'ß':
		return 's'
	case 'æ', 'Æ':
		return 'A'
	case 'œ', 'Œ':
		return 'O'
	case '€':
		return 'E'
	case '£':
		return 'L'
	case '°':
		return 'o'
	}
	return '?'
}

// EncodeReceiptText converts receipt text to printer-safe bytes: CR/LF
// normalized to LF, non-ASCII folded per latin1Fold.
func EncodeReceiptText(s string) []byte {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r == '\n' {
			out = append(out, '\n')
			continue
		}
		out = append(out, latin1Fold(r))
	}
	return out
}

// ESCPosJob assembles a complete downloadable printer job: initialize,
// optional drawer kick, receipt text, line feeds, paper cut.
func ESCPosJob(text string, kickDrawer, fullCut bool) []byte {
	var job []byte
	job = append(job, InitBytes()...)
	if kickDrawer {
		job = append(job, DrawerKickBytes()...)
	}
	job = append(job, EncodeReceiptText(text)...)
	job = append(job, '\n', '\n', '\n')
	job = append(job, CutBytes(fullCut)...)
	return job
}
