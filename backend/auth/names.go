package auth

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	minUsernameLen = 2
	maxUsernameLen = 20
)

// reservedNames can't be taken: "system" is who the table's messages are from.
var reservedNames = map[string]bool{"system": true}

// NormalizeUsername checks a username and returns it as it will be shown, and
// the key that uniqueness is checked on, so Alice and alice are the same name.
func NormalizeUsername(s string) (display, key string, err error) {
	display = norm.NFC.String(s)
	n := utf8.RuneCountInString(display)
	if n < minUsernameLen || n > maxUsernameLen {
		return "", "", ErrBadUsername
	}
	for _, r := range display {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) && r != '_' && r != '-' {
			return "", "", ErrBadUsername
		}
	}
	key = strings.ToLower(display)
	if reservedNames[key] {
		return "", "", ErrBadUsername
	}
	return display, key, nil
}
