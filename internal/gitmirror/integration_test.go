package gitmirror

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestModuleClonesAndPushesExactMirrorRefs verifies the module interface against real local Git repositories without network access.
func TestModuleClonesAndPushesExactMirrorRefs(t *testing.T) {
	requireGit(t)
	root := filepath.Join(t.TempDir(), "local Git repositories")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	source := filepath.Join(root, "source worktree")
	runGit(t, "init", source)
	runGit(t, "-C", source, "config", "user.name", "Forge Rescue Test")
	runGit(t, "-C", source, "config", "user.email", "forge-rescue@example.invalid")
	runGit(t, "-C", source, "config", "commit.gpgSign", "false")
	runGit(t, "-C", source, "config", "tag.gpgSign", "false")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("rescued\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	runGit(t, "-C", source, "add", "README.md")
	runGit(t, "-C", source, "commit", "-m", "initial")
	runGit(t, "-C", source, "tag", "v1.0.0")
	runGit(t, "-C", source, "branch", "rescue-copy")

	sourceRemote, err := NewRemote(source)
	if err != nil {
		t.Fatalf("NewRemote returned error: %v", err)
	}
	module := New()
	mirrorPath := filepath.Join(root, "rescued mirror.git")
	if err := module.Clone(context.Background(), sourceRemote, mirrorPath); err != nil {
		t.Fatalf("Clone returned error: %v", err)
	}
	if _, err := module.Open(context.Background(), mirrorPath); err != nil {
		t.Fatalf("Open returned error: %v", err)
	}

	destinationPath := filepath.Join(root, "destination mirror.git")
	runGit(t, "init", "--bare", destinationPath)
	destination, err := NewRemote(destinationPath)
	if err != nil {
		t.Fatalf("NewRemote destination returned error: %v", err)
	}
	if err := module.Push(context.Background(), mirrorPath, destination); err != nil {
		t.Fatalf("Push returned error: %v", err)
	}
	assertRefExists(t, destinationPath, "refs/heads/rescue-copy")
	assertRefExists(t, destinationPath, "refs/tags/v1.0.0")

	runGit(t, "-C", mirrorPath, "update-ref", "-d", "refs/heads/rescue-copy")
	if err := module.Push(context.Background(), mirrorPath, destination); err != nil {
		t.Fatalf("second Push returned error: %v", err)
	}
	assertRefMissing(t, destinationPath, "refs/heads/rescue-copy")
}

// TestOpenRejectsOrdinaryDirectory verifies filesystem presence alone never qualifies an artifact as a mirror.
func TestOpenRejectsOrdinaryDirectory(t *testing.T) {
	requireGit(t)
	_, err := New().Open(context.Background(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "validate mirror failed") {
		t.Fatalf("Open error = %v, want Git validation failure", err)
	}
}

// requireGit skips integration checks only when the application's required Git executable is unavailable.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable is unavailable")
	}
}

// runGit runs fixture setup commands and reports their combined output on failure.
func runGit(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

// assertRefExists verifies that mirror push copied a named source ref exactly.
func assertRefExists(t *testing.T, repository, ref string) {
	t.Helper()
	runGit(t, "-C", repository, "show-ref", "--verify", ref)
}

// assertRefMissing verifies that mirror push deleted a destination ref removed from the source mirror.
func assertRefMissing(t *testing.T, repository, ref string) {
	t.Helper()
	cmd := exec.Command("git", "-C", repository, "show-ref", "--verify", ref)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("ref %q still exists: %s", ref, output)
	}
}
