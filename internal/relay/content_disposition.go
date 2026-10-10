package relay

import (
	"fmt"
	"strings"
)

func contentDisposition(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 32 || r > 126 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf("attachment; filename=\"%s\"; filename*=UTF-8''%s", ascii, extValueEscape(name))
}

// extValueEscape percent encodes everything outside RFC 5987's attr-char set.
// url.PathEscape isn't strict enough: it leaves ; , ' ( ) and friends alone
func extValueEscape(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isAttrChar(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0xf])
	}
	return b.String()
}

func isAttrChar(c byte) bool {
	if ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9') {
		return true
	}
	return strings.IndexByte("!#$&+-.^_`|~", c) >= 0
}
