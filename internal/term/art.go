// SPDX-License-Identifier: GPL-3.0-or-later

package term

import (
	"bytes"
	"strconv"
	"unicode/utf8"
)

const (
	dosEOF         = 0x1a
	maxCursorShift = 255
	ansiReset      = "\x1b[0m"
)

// StripSAUCE drops the SAUCE metadata record art editors append, which
// starts at the DOS end-of-file marker.
func StripSAUCE(data []byte) []byte {
	if i := bytes.IndexByte(data, dosEOF); i >= 0 {
		return data[:i]
	}
	return data
}

// ConvertANSIArt prepares a CP437 ANSI art file (as saved by PabloDraw,
// Moebius and friends) for a terminal:
//
//   - CP437 terminals get the original bytes.
//   - UTF-8 terminals get the same art with glyphs converted to Unicode.
//   - ASCII terminals get approximated glyphs; without color, escape
//     sequences are dropped, except cursor-forward, which becomes spaces.
//
// Line endings become CR LF and colors are reset at the end.
func ConvertANSIArt(data []byte, cs Charset, color bool) []byte {
	data = StripSAUCE(data)
	out := make([]byte, 0, len(data)+len(data)/2)
	for i := 0; i < len(data); i++ {
		b := data[i]
		if b == 0x1b && i+1 < len(data) && data[i+1] == '[' {
			end := csiEnd(data, i+2)
			if end < 0 {
				break // truncated sequence at end of file
			}
			out = appendCSI(out, data[i:end+1], color)
			i = end
			continue
		}
		switch {
		case b == '\n':
			if i == 0 || data[i-1] != '\r' {
				out = append(out, '\r')
			}
			out = append(out, '\n')
		case b == '\r' || b == '\t':
			out = append(out, b)
		case b < 0x20 || b == 0x7f:
			// other control bytes: bells, form feeds and the like
		case cs == CP437:
			out = append(out, b)
		case cs == UTF8:
			out = utf8.AppendRune(out, DecodeCP437(b))
		default:
			out = append(out, Encode(ASCII, string(DecodeCP437(b)))...)
		}
	}
	if color {
		out = append(out, ansiReset...)
	}
	return out
}

// csiEnd returns the index of the final byte of a CSI sequence whose
// parameters start at from, or -1 if the data ends first.
func csiEnd(data []byte, from int) int {
	for j := from; j < len(data); j++ {
		if data[j] >= 0x40 && data[j] <= 0x7e {
			return j
		}
	}
	return -1
}

func appendCSI(out, seq []byte, color bool) []byte {
	if color {
		return append(out, seq...)
	}
	if seq[len(seq)-1] != 'C' { // only cursor-forward survives, as spaces
		return out
	}
	n := 1
	if params := string(seq[2 : len(seq)-1]); params != "" {
		if v, err := strconv.Atoi(params); err == nil {
			n = v
		}
	}
	return append(out, bytes.Repeat([]byte{' '}, min(max(n, 0), maxCursorShift))...)
}
