package agentstatus

import (
	"strings"
	"unicode"
)

// CurrentWorkPreview is a bounded display of retained self-report, never an
// observation of process activity. The complete account is left unchanged.
func CurrentWorkPreview(account *Account) string {
	if account == nil || len(account.Current) == 0 {
		return ""
	}
	text := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return ' '
		}
		return r
	}, account.Current[0])
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= 160 {
		return text
	}
	var out strings.Builder
	for _, r := range text {
		if out.Len()+len(string(r)) > 157 {
			break
		}
		out.WriteRune(r)
	}
	return out.String() + "…"
}
