package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestBrowserStartsXDGOpenWithoutAShell(t *testing.T) {
	var command string
	var arguments []string
	starter := func(name string, args ...string) error {
		command = name
		arguments = append([]string(nil), args...)
		return nil
	}

	err := openBrowserWith("https://auth.example/authorize?state=fake", starter)
	if err != nil {
		t.Fatalf("openBrowserWith() error = %v", err)
	}
	if command != "xdg-open" {
		t.Fatalf("command = %q, want xdg-open", command)
	}
	if len(arguments) != 1 || arguments[0] != "https://auth.example/authorize?state=fake" {
		t.Fatalf("arguments = %#v", arguments)
	}
}

func TestBrowserRejectsUnsafeOrMalformedURLsBeforeStartingCommand(t *testing.T) {
	testCases := []string{
		"",
		"relative/path",
		"file:///tmp/authorization",
		"javascript:alert(1)",
		"https:///missing-host",
		"https://user:password@auth.example/authorize",
		"https://auth.example/authorize\n--malicious",
	}

	for _, rawURL := range testCases {
		t.Run(rawURL, func(t *testing.T) {
			called := false
			err := openBrowserWith(rawURL, func(string, ...string) error {
				called = true
				return nil
			})
			if err == nil {
				t.Fatalf("openBrowserWith(%q) succeeded", rawURL)
			}
			if called {
				t.Fatal("invalid URL started a command")
			}
		})
	}
}

func TestBrowserReturnsCommandStartFailure(t *testing.T) {
	err := openBrowserWith(
		"http://127.0.0.1:8080/authorize",
		func(string, ...string) error {
			return errors.New("fake start failure")
		},
	)
	if err == nil || !strings.Contains(err.Error(), "fake start failure") {
		t.Fatalf("openBrowserWith() error = %v", err)
	}
}

func TestBrowserPublicFunctionValidatesBeforeLaunching(t *testing.T) {
	err := OpenBrowser("file:///tmp/not-an-oauth-url")
	if err == nil {
		t.Fatal("OpenBrowser accepted a non-HTTP URL")
	}
}
