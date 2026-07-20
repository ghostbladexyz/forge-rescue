// Package gitmirror owns safe, noninteractive Git mirror transport.
package gitmirror

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Module hides Git process construction, validation, credentials, and cleanup behind mirror operations.
type Module struct {
	runner processRunner
}

// Remote holds a clean Git URL and optional operation-scoped credentials.
type Remote struct {
	rawURL     string
	credential *credential
}

type credential struct {
	username string
	password string
}

// Mirror identifies a local path only after Git has confirmed that it is a bare repository.
type Mirror struct {
	path string
}

// New constructs the production Git mirror module.
func New() *Module {
	return &Module{runner: execProcessRunner{}}
}

// NewRemote validates a clean remote URL that relies on existing Git credential configuration when needed.
func NewRemote(rawURL string) (Remote, error) {
	if strings.TrimSpace(rawURL) == "" {
		return Remote{}, fmt.Errorf("git remote URL is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return Remote{}, fmt.Errorf("parse git remote URL")
	}
	if parsed.User != nil {
		return Remote{}, fmt.Errorf("git remote URL must not contain credentials")
	}
	if (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host == "" {
		return Remote{}, fmt.Errorf("git HTTP remote URL must include a host")
	}
	if (parsed.Scheme == "http" || parsed.Scheme == "https") && (parsed.RawQuery != "" || parsed.Fragment != "") {
		return Remote{}, fmt.Errorf("git HTTP remote URL must not contain a query or fragment")
	}
	return Remote{rawURL: rawURL}, nil
}

// NewAuthenticatedRemote validates a clean remote and attaches credentials that exist only for one Git operation.
func NewAuthenticatedRemote(rawURL, username, password string) (Remote, error) {
	remote, err := NewRemote(rawURL)
	if err != nil {
		return Remote{}, err
	}
	if username == "" || password == "" {
		return Remote{}, fmt.Errorf("git remote username and password are required")
	}
	if strings.ContainsAny(username, "\x00\r\n") || strings.ContainsAny(password, "\x00\r\n") {
		return Remote{}, fmt.Errorf("git remote credentials contain unsupported control characters")
	}
	remote.credential = &credential{username: username, password: password}
	return remote, nil
}

// Clone atomically creates and validates a bare mirror at the workspace-provided destination.
func (m *Module) Clone(ctx context.Context, source Remote, destination string) error {
	if source.rawURL == "" {
		return fmt.Errorf("git clone remote is required")
	}
	if err := validateDestination(destination); err != nil {
		return err
	}
	parent := filepath.Dir(destination)
	temporary, err := os.MkdirTemp(parent, "."+filepath.Base(destination)+".clone-")
	if err != nil {
		return fmt.Errorf("prepare temporary mirror: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(temporary) // A failed or cancelled clone must not look like a resumable mirror.
		}
	}()

	if _, err := m.run(ctx, "clone mirror", source, "clone", "--mirror", source.rawURL, temporary); err != nil {
		return err
	}
	if _, err := m.open(ctx, temporary); err != nil {
		return fmt.Errorf("validate cloned mirror: %w", err)
	}
	if err := os.Rename(temporary, destination); err != nil {
		return fmt.Errorf("commit cloned mirror: %w", err)
	}
	committed = true
	return nil
}

// Open validates a workspace-provided path and returns a mirror handle only for a bare Git repository.
func (m *Module) Open(ctx context.Context, path string) (Mirror, error) {
	return m.open(ctx, path)
}

// Push validates a local mirror before synchronizing all refs to the supplied clean remote.
func (m *Module) Push(ctx context.Context, mirrorPath string, destination Remote) error {
	if destination.rawURL == "" {
		return fmt.Errorf("git push remote is required")
	}
	mirror, err := m.open(ctx, mirrorPath)
	if err != nil {
		return err
	}
	_, err = m.run(ctx, "push mirror", destination, "-C", mirror.path, "push", "--mirror", destination.rawURL)
	return err
}

// open checks both filesystem shape and Git's bare-repository flag because an arbitrary directory is not a rescued mirror.
func (m *Module) open(ctx context.Context, path string) (Mirror, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Mirror{}, fmt.Errorf("inspect local mirror: %w", err)
	}
	if !info.IsDir() {
		return Mirror{}, fmt.Errorf("local mirror is not a directory")
	}
	result, err := m.run(ctx, "validate mirror", Remote{}, "-C", path, "rev-parse", "--is-bare-repository")
	if err != nil {
		return Mirror{}, err
	}
	if strings.TrimSpace(string(result.stdout)) != "true" {
		return Mirror{}, fmt.Errorf("local mirror is not a bare Git repository")
	}
	return Mirror{path: path}, nil
}

// validateDestination fails closed when any filesystem object already occupies the workspace-owned destination.
func validateDestination(destination string) error {
	if strings.TrimSpace(destination) == "" {
		return fmt.Errorf("mirror destination is required")
	}
	_, err := os.Lstat(destination)
	if err == nil {
		return fmt.Errorf("mirror destination already exists")
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect mirror destination: %w", err)
	}
	parent, err := os.Stat(filepath.Dir(destination))
	if err != nil {
		return fmt.Errorf("inspect mirror parent: %w", err)
	}
	if !parent.IsDir() {
		return fmt.Errorf("mirror parent is not a directory")
	}
	return nil
}
