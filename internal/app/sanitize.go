package app

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// sanitizeRemoteText removes bytes that could be interpreted as terminal
// commands while preserving printable Unicode and intentional line breaks.
func sanitizeRemoteText(value string) string {
	value = ansi.Strip(value)

	var sanitized strings.Builder
	sanitized.Grow(len(value))
	for _, character := range value {
		switch character {
		case '\n':
			sanitized.WriteByte('\n')
		case '\t':
			sanitized.WriteString("    ")
		default:
			if unicode.IsControl(character) {
				continue
			}
			sanitized.WriteRune(character)
		}
	}
	return sanitized.String()
}

func sanitizeRemoteLine(value string) string {
	return strings.ReplaceAll(sanitizeRemoteText(value), "\n", " ")
}
