// Package auth implements traq-tui's OAuth login and token persistence.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/oauth2"
)

const maximumTokenFileSize = 1 << 20

// ErrTokenNotFound indicates that no persisted OAuth token exists.
var ErrTokenNotFound = errors.New("OAuth token not found")

// TokenStore persists OAuth tokens without exposing their contents.
type TokenStore interface {
	Load(context.Context) (*oauth2.Token, error)
	Save(context.Context, *oauth2.Token) error
	Delete(context.Context) error
}

type fileTokenStore struct {
	path string
}

// NewFileTokenStore returns a token store backed by path.
func NewFileTokenStore(path string) TokenStore {
	return &fileTokenStore{path: filepath.Clean(path)}
}

func (s *fileTokenStore) Load(ctx context.Context) (*oauth2.Token, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	info, err := os.Lstat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrTokenNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("inspect token file: %w", err)
	}
	if err := validatePrivateTokenDirectory(filepath.Dir(s.path)); err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("token file must not be a symbolic link")
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("token file must be a regular file")
	}
	if permissions := info.Mode().Perm(); permissions != 0o600 {
		return nil, fmt.Errorf(
			"token file permissions must be 0600; got %04o",
			permissions,
		)
	}

	file, err := os.Open(s.path)
	if err != nil {
		return nil, fmt.Errorf("open token file: %w", err)
	}
	defer file.Close()

	var token oauth2.Token
	decoder := json.NewDecoder(io.LimitReader(file, maximumTokenFileSize))
	if err := decoder.Decode(&token); err != nil {
		return nil, fmt.Errorf("decode token file: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("decode token file: multiple JSON values")
		}
		return nil, fmt.Errorf("decode token file: %w", err)
	}
	if token.AccessToken == "" {
		return nil, errors.New("token file does not contain an access token")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &token, nil
}

func (s *fileTokenStore) Save(ctx context.Context, token *oauth2.Token) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if token == nil {
		return errors.New("cannot save a nil OAuth token")
	}
	if token.AccessToken == "" {
		return errors.New("cannot save an OAuth token without an access token")
	}

	directory := filepath.Dir(s.path)
	if err := ensurePrivateTokenDirectory(directory); err != nil {
		return err
	}

	if info, err := os.Lstat(s.path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("token file must not be a symbolic link")
		}
		if !info.Mode().IsRegular() {
			return errors.New("token file must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect token file: %w", err)
	}

	temporary, err := os.CreateTemp(directory, ".token-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary token file: %w", err)
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure temporary token file: %w", err)
	}
	if err := json.NewEncoder(temporary).Encode(token); err != nil {
		return fmt.Errorf("encode token file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync token file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close token file: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		return fmt.Errorf("replace token file: %w", err)
	}
	removeTemporary = false

	directoryHandle, err := os.Open(directory)
	if err == nil {
		_ = directoryHandle.Sync()
		_ = directoryHandle.Close()
	}
	return nil
}

func ensurePrivateTokenDirectory(directory string) error {
	return checkPrivateTokenDirectory(directory, true)
}

func validatePrivateTokenDirectory(directory string) error {
	return checkPrivateTokenDirectory(directory, false)
}

func checkPrivateTokenDirectory(directory string, create bool) error {
	directory = filepath.Clean(directory)
	if err := checkDirectoryPath(directory, create); err != nil {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect token directory: %w", err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o700 {
		return fmt.Errorf(
			"token directory permissions must be 0700; got %04o",
			permissions,
		)
	}
	return nil
}

func checkDirectoryPath(directory string, create bool) error {
	parent := filepath.Dir(directory)
	if parent != directory {
		if err := checkDirectoryPath(parent, create); err != nil {
			return err
		}
	}

	info, err := os.Lstat(directory)
	if create && errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("create token directory: %w", err)
		}
		info, err = os.Lstat(directory)
	}
	if err != nil {
		return fmt.Errorf("inspect token directory path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("token directory path must not contain a symbolic link")
	}
	if !info.IsDir() {
		return errors.New("token directory path must contain only directories")
	}
	return nil
}

func (s *fileTokenStore) Delete(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect token file: %w", err)
	}
	if err := validatePrivateTokenDirectory(filepath.Dir(s.path)); err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("token file must not be a symbolic link")
	}
	if !info.Mode().IsRegular() {
		return errors.New("token file must be a regular file")
	}
	if err := os.Remove(s.path); err != nil {
		return fmt.Errorf("delete token file: %w", err)
	}
	return nil
}
