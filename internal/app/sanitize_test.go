package app

import (
	"strings"
	"testing"
)

func TestSanitizeRemoteTextRemovesTerminalControls(t *testing.T) {
	input := "安全 safe\x1b[31mred\x1b[0m\rreplace\x07\tok\nnext\x00"

	got := sanitizeRemoteText(input)
	want := "安全 saferedreplace    ok\nnext"
	if got != want {
		t.Fatalf("sanitizeRemoteText() = %q, want %q", got, want)
	}
	if strings.ContainsAny(got, "\x1b\r\a\x00") {
		t.Fatalf("unsafe output %q", got)
	}
}

func TestSanitizeRemoteTextRemovesOSCHyperlinksButKeepsLabel(t *testing.T) {
	input := "\x1b]8;;https://attacker.invalid/\x1b\\click me\x1b]8;;\x1b\\"

	got := sanitizeRemoteText(input)
	if got != "click me" {
		t.Fatalf("sanitizeRemoteText() = %q, want %q", got, "click me")
	}
	if strings.Contains(got, "attacker.invalid") || strings.ContainsRune(got, '\x1b') {
		t.Fatalf("OSC hyperlink survived sanitization: %q", got)
	}
}

func TestSanitizeRemoteTextDropsNonPrintingC0C1AndDelete(t *testing.T) {
	input := "a\x01b\x1fc\x7fd\u0085e"

	got := sanitizeRemoteText(input)
	if got != "abcde" {
		t.Fatalf("sanitizeRemoteText() = %q, want %q", got, "abcde")
	}
}
