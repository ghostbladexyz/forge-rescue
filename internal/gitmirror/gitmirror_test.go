package gitmirror

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type scriptedRunner struct {
	invocations []processInvocation
	results     []processResult
	errors      []error
}

// Run records module-private process intent and returns the next scripted result.
func (r *scriptedRunner) Run(ctx context.Context, invocation processInvocation) (processResult, error) {
	r.invocations = append(r.invocations, invocation)
	index := len(r.invocations) - 1
	var result processResult
	if index < len(r.results) {
		result = r.results[index]
	}
	var err error
	if index < len(r.errors) {
		err = r.errors[index]
	}
	return result, err
}

// TestNewRemoteRejectsCredentialsInURL verifies malformed input cannot move a secret into Git arguments or validation errors.
func TestNewRemoteRejectsCredentialsInURL(t *testing.T) {
	const secret = "super-secret-token"
	_, err := NewRemote("https://user:" + secret + "@github.com/owner/repo.git")
	if err == nil {
		t.Fatal("NewRemote returned nil, want embedded-credential error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("NewRemote error exposed secret: %v", err)
	}
}

// TestNewRemoteRejectsHTTPQuery verifies token-like query data cannot enter Git process arguments.
func TestNewRemoteRejectsHTTPQuery(t *testing.T) {
	const secret = "query-secret"
	_, err := NewRemote("https://github.com/owner/repo.git?token=" + secret)
	if err == nil {
		t.Fatal("NewRemote returned nil, want query rejection")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("NewRemote error exposed query data: %v", err)
	}
}

// TestAuthenticatedInvocationKeepsSecretsOutOfArguments verifies askpass receives credentials only through the child environment.
func TestAuthenticatedInvocationKeepsSecretsOutOfArguments(t *testing.T) {
	const username = "x-access-token"
	const password = "github-secret"
	remote, err := NewAuthenticatedRemote("https://github.com/owner/repo.git", username, password)
	if err != nil {
		t.Fatalf("NewAuthenticatedRemote returned error: %v", err)
	}
	invocation, err := newInvocation(remote, t.TempDir(), []string{"push", "--mirror", remote.rawURL})
	if err != nil {
		t.Fatalf("newInvocation returned error: %v", err)
	}
	arguments := strings.Join(invocation.args, " ")
	if strings.Contains(arguments, username) || strings.Contains(arguments, password) {
		t.Fatalf("Git arguments exposed credentials: %q", arguments)
	}
	environment := strings.Join(invocation.env, "\n")
	if !strings.Contains(environment, askPassUsername+"="+username) || !strings.Contains(environment, askPassPassword+"="+password) {
		t.Fatal("askpass environment did not receive operation-scoped credentials")
	}
}

// TestRunRedactsCredentialDiagnostics verifies raw Git output cannot leak credentials through returned errors.
func TestRunRedactsCredentialDiagnostics(t *testing.T) {
	const username = "credential-user"
	const password = "credential-password"
	remote, err := NewAuthenticatedRemote("https://github.com/owner/repo.git", username, password)
	if err != nil {
		t.Fatalf("NewAuthenticatedRemote returned error: %v", err)
	}
	runner := &scriptedRunner{
		results: []processResult{{stderr: []byte("fatal: " + username + ":" + password)}},
		errors:  []error{errors.New("process failed")},
	}
	module := &Module{runner: runner}
	_, err = module.run(context.Background(), "test operation", remote, "push", remote.rawURL)
	if err == nil {
		t.Fatal("run returned nil, want process failure")
	}
	if strings.Contains(err.Error(), username) || strings.Contains(err.Error(), password) {
		t.Fatalf("returned error exposed credentials: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("returned error = %v, want redaction marker", err)
	}
}

// TestRunPreservesCancellation verifies callers can distinguish cancellation without receiving raw process errors.
func TestRunPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	module := &Module{runner: &scriptedRunner{errors: []error{context.Canceled}}}
	_, err := module.run(ctx, "cancelled operation", Remote{}, "status")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v, want context.Canceled", err)
	}
}

// TestCloneCleansTemporaryDirectoryAfterValidationFailure verifies an incomplete clone never occupies the durable destination.
func TestCloneCleansTemporaryDirectoryAfterValidationFailure(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "repo.git")
	remote, err := NewRemote("https://git.example/owner/repo.git")
	if err != nil {
		t.Fatalf("NewRemote returned error: %v", err)
	}
	runner := &scriptedRunner{results: []processResult{{}, {stdout: []byte("false\n")}}}
	module := &Module{runner: runner}
	if err := module.Clone(context.Background(), remote, destination); err == nil {
		t.Fatal("Clone returned nil, want bare validation failure")
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination survived failed clone: %v", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("ReadDir returned error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary clone entries = %#v, want cleanup", entries)
	}
}

// TestCloneRefusesExistingDestination verifies atomic clone never overwrites a workspace artifact.
func TestCloneRefusesExistingDestination(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "repo.git")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatalf("Mkdir returned error: %v", err)
	}
	remote, err := NewRemote("https://git.example/owner/repo.git")
	if err != nil {
		t.Fatalf("NewRemote returned error: %v", err)
	}
	runner := &scriptedRunner{}
	module := &Module{runner: runner}
	if err := module.Clone(context.Background(), remote, destination); err == nil {
		t.Fatal("Clone returned nil, want existing destination error")
	}
	if len(runner.invocations) != 0 {
		t.Fatalf("process invocations = %d, want none", len(runner.invocations))
	}
}

// TestBoundedBufferTruncatesDiagnostics verifies remote output remains bounded in memory and visibly marked.
func TestBoundedBufferTruncatesDiagnostics(t *testing.T) {
	buffer := newBoundedBuffer(4)
	if _, err := buffer.Write([]byte("abcdefgh")); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if got := string(buffer.Bytes()); got != "abcd\n[output truncated]" {
		t.Fatalf("Bytes = %q, want bounded output", got)
	}
}

// TestHandleAskPassChoosesPromptValue verifies the helper separates Git's username and password prompts.
func TestHandleAskPassChoosesPromptValue(t *testing.T) {
	t.Setenv(askPassMarker, "1")
	t.Setenv(askPassUsername, "askpass-user")
	t.Setenv(askPassPassword, "askpass-password")
	var username strings.Builder
	if !HandleAskPass([]string{"forge-rescue", "Username for remote"}, &username) || username.String() != "askpass-user\n" {
		t.Fatalf("username answer = %q", username.String())
	}
	var password strings.Builder
	if !HandleAskPass([]string{"forge-rescue", "Password for remote"}, &password) || password.String() != "askpass-password\n" {
		t.Fatalf("password answer = %q", password.String())
	}
}
