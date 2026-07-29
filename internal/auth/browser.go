package auth

import (
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

type commandStarter func(string, ...string) error

// OpenBrowser starts the Linux desktop URL opener for an OAuth authorization
// URL. It never invokes a shell.
func OpenBrowser(rawURL string) error {
	return openBrowserWith(rawURL, startCommand)
}

func openBrowserWith(rawURL string, start commandStarter) error {
	if start == nil {
		return errors.New("browser command starter is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.Opaque != "" ||
		(!strings.EqualFold(parsed.Scheme, "http") &&
			!strings.EqualFold(parsed.Scheme, "https")) {
		return errors.New("authorization URL must be an absolute http or https URL")
	}
	if err := start("xdg-open", rawURL); err != nil {
		return fmt.Errorf("start xdg-open: %w", err)
	}
	return nil
}

func startCommand(name string, arguments ...string) error {
	command := exec.Command(name, arguments...)
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
