package main

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"strings"
	"unicode/utf8"
)

// decodeAttributedBody extracts the plain string from a message.attributedBody
// blob. Since macOS Ventura, Messages often leaves message.text NULL and stores
// the content only here, as an NSAttributedString archived in Apple's legacy
// "typedstream" format.
//
// We don't implement a full typedstream parser. The archive always contains
// the NSString class name, followed shortly by a '+' type tag (C string), a
// length, and the UTF-8 bytes. That's enough to recover the text reliably.
//
// Length encoding: a single byte < 0x80 is the length itself; 0x81 means an
// int16 little-endian follows; 0x82 means an int32 little-endian follows.
func decodeAttributedBody(blob []byte) string {
	idx := bytes.Index(blob, []byte("NSString"))
	if idx < 0 {
		return ""
	}
	rest := blob[idx+len("NSString"):]

	// The '+' tag sits a few bytes after the class name (after the class
	// version and some typedstream markers). Search a small window for it.
	plus := bytes.IndexByte(rest[:min(len(rest), 16)], '+')
	if plus < 0 {
		return ""
	}
	rest = rest[plus+1:]
	if len(rest) == 0 {
		return ""
	}

	var n int
	switch rest[0] {
	case 0x81:
		if len(rest) < 3 {
			return ""
		}
		n = int(binary.LittleEndian.Uint16(rest[1:3]))
		rest = rest[3:]
	case 0x82:
		if len(rest) < 5 {
			return ""
		}
		n = int(binary.LittleEndian.Uint32(rest[1:5]))
		rest = rest[5:]
	default:
		n = int(rest[0])
		rest = rest[1:]
	}
	if n <= 0 || n > len(rest) {
		return ""
	}
	s := rest[:n]
	if !utf8.Valid(s) {
		return strings.ToValidUTF8(string(s), "")
	}
	return string(s)
}

// messageText returns the displayable text of a message row: message.text
// when it has content, otherwise the string decoded from attributedBody.
// U+FFFC (the inline placeholder Messages uses for attachments) is stripped
// from either source.
func messageText(text sql.NullString, attributedBody []byte) string {
	t := strings.TrimSpace(strings.ReplaceAll(text.String, "￼", ""))
	if t == "" && len(attributedBody) > 0 {
		t = strings.TrimSpace(strings.ReplaceAll(decodeAttributedBody(attributedBody), "￼", ""))
	}
	return t
}
