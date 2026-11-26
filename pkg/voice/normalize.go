package voice

import (
	"strings"
	"unicode"
)

func Normalize(text string) string { return NormalizeIn(Current(), text) }

func NormalizeIn(l Language, text string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'':
			return unicode.ToLower(r)
		default:
			return ' '
		}
	}, text)
	return strings.Join(l.Numbers(strings.Fields(clean)), " ")
}

func TrimArticles(l Language, words string) string {
	fields := strings.Fields(words)
	if len(fields) == 0 {
		return words
	}
	for _, a := range l.Articles() {
		if fields[0] == a && len(fields) > 1 {
			return strings.Join(fields[1:], " ")
		}
		if strings.HasSuffix(a, "'") {
			if rest, ok := strings.CutPrefix(fields[0], a); ok && rest != "" {
				fields[0] = rest
				return strings.Join(fields, " ")
			}
		}
	}
	return words
}
