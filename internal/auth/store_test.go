package auth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestFileTokenStoreReportsMissingToken(t *testing.T) {
	store := NewFileTokenStore(filepath.Join(t.TempDir(), "token.json"))

	_, err := store.Load(context.Background())
	if !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("Load error = %v, want ErrTokenNotFound", err)
	}
}

func TestFileTokenStoreRoundTripUsesPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "token.json")
	store := NewFileTokenStore(path)
	want := &oauth2.Token{
		AccessToken:  "fake-access-token",
		TokenType:    "Bearer",
		RefreshToken: "fake-refresh-token",
		Expiry:       time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
	}

	if err := store.Save(context.Background(), want); err != nil {
		t.Fatal(err)
	}

	directoryInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := directoryInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("directory mode = %o, want 700", got)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode = %o, want 600", got)
	}

	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != want.AccessToken ||
		got.RefreshToken != want.RefreshToken ||
		got.TokenType != want.TokenType ||
		!got.Expiry.Equal(want.Expiry) {
		t.Fatalf("loaded token differs from saved token")
	}
}

func TestFileTokenStoreDoesNotChangeExistingParentPermissions(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "token.json")
	store := NewFileTokenStore(path)

	err := store.Save(context.Background(), &oauth2.Token{AccessToken: "fake-token"})
	if err == nil || !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("Save error = %v, want private-directory permission error", err)
	}

	info, statErr := os.Stat(parent)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("parent mode was changed to %o", got)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("token was written despite unsafe directory: %v", statErr)
	}
}

func TestFileTokenStoreRejectsSymlinkInParentPath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(link, "token.json")
	store := NewFileTokenStore(path)

	err := store.Save(context.Background(), &oauth2.Token{AccessToken: "fake-token"})
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Save error = %v, want parent symlink rejection", err)
	}
	if _, statErr := os.Stat(filepath.Join(target, "token.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("token was written through parent symlink: %v", statErr)
	}
}

func TestFileTokenStoreLoadRejectsSymlinkInParentPath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(target, "token.json"),
		[]byte(`{"access_token":"fake-attacker-token"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	store := NewFileTokenStore(filepath.Join(link, "token.json"))

	_, err := store.Load(context.Background())
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Load error = %v, want parent symlink rejection", err)
	}
}

func TestFileTokenStoreDeleteRejectsSymlinkInParentPath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	targetToken := filepath.Join(target, "token.json")
	if err := os.WriteFile(
		targetToken,
		[]byte(`{"access_token":"fake-target-token"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	store := NewFileTokenStore(filepath.Join(link, "token.json"))

	err := store.Delete(context.Background())
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Delete error = %v, want parent symlink rejection", err)
	}
	if _, statErr := os.Stat(targetToken); statErr != nil {
		t.Fatalf("Delete removed target through parent symlink: %v", statErr)
	}
}

func TestFileTokenStoreAtomicallyReplacesExistingToken(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "token.json")
	store := NewFileTokenStore(path)

	if err := store.Save(context.Background(), &oauth2.Token{AccessToken: "fake-old-token"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), &oauth2.Token{AccessToken: "fake-new-token"}); err != nil {
		t.Fatal(err)
	}

	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "fake-new-token" {
		t.Fatalf("access token was not replaced")
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "token.json" {
		t.Fatalf("unexpected files after replacement: %v", entries)
	}
}

func TestFileTokenStoreRejectsCorruptJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewFileTokenStore(path)

	_, err := store.Load(context.Background())
	if err == nil {
		t.Fatal("expected corrupt token error")
	}
	if errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("corrupt file reported as missing: %v", err)
	}
}

func TestFileTokenStoreLoadRejectsPublicFilePermissions(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "token.json")
	if err := os.WriteFile(
		path,
		[]byte(`{"access_token":"fake-exposed-token"}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewFileTokenStore(path)

	_, err := store.Load(context.Background())
	if err == nil || !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("Load error = %v, want private-file permission error", err)
	}
}

func TestFileTokenStoreRejectsSymlinkTarget(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.json")
	if err := os.WriteFile(target, []byte(`{"access_token":"fake-original"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "token.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	store := NewFileTokenStore(link)

	err := store.Save(context.Background(), &oauth2.Token{AccessToken: "fake-replacement"})
	if err == nil {
		t.Fatal("expected symlink rejection")
	}

	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "fake-replacement") {
		t.Fatal("symlink target was overwritten")
	}
}

func TestFileTokenStoreDeleteIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "token.json")
	store := NewFileTokenStore(path)

	if err := store.Delete(context.Background()); err != nil {
		t.Fatalf("delete missing token: %v", err)
	}
	if err := store.Save(context.Background(), &oauth2.Token{AccessToken: "fake-token"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("token still exists: %v", err)
	}
	if err := store.Delete(context.Background()); err != nil {
		t.Fatalf("second delete: %v", err)
	}
}

func TestFileTokenStoreHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := NewFileTokenStore(filepath.Join(t.TempDir(), "token.json"))

	if err := store.Save(ctx, &oauth2.Token{AccessToken: "fake-token"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Save error = %v, want context.Canceled", err)
	}
	if _, err := store.Load(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load error = %v, want context.Canceled", err)
	}
	if err := store.Delete(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete error = %v, want context.Canceled", err)
	}
}

func TestFileTokenStoreRejectsNilToken(t *testing.T) {
	store := NewFileTokenStore(filepath.Join(t.TempDir(), "token.json"))

	if err := store.Save(context.Background(), nil); err == nil {
		t.Fatal("expected nil token error")
	}
}
