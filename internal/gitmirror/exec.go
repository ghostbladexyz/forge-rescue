package gitmirror

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

const (
	maxCommandOutput = 16 * 1024
	askPassMarker    = "FORGE_RESCUE_GIT_ASKPASS"
	askPassUsername  = "FORGE_RESCUE_GIT_USERNAME"
	askPassPassword  = "FORGE_RESCUE_GIT_PASSWORD"
)

type processInvocation struct {
	args    []string
	env     []string
	secrets []string
}

type processResult struct {
	stdout []byte
	stderr []byte
}

type processRunner interface {
	Run(ctx context.Context, invocation processInvocation) (processResult, error)
}

type execProcessRunner struct{}

type commandError struct {
	operation string
	detail    string
	cause     error
}

type boundedBuffer struct {
	data      bytes.Buffer
	remaining int
	truncated bool
}

// HandleAskPass answers Git's credential prompt only when an authenticated mirror operation marks this process as its askpass child.
func HandleAskPass(args []string, out io.Writer) bool {
	if os.Getenv(askPassMarker) != "1" {
		return false
	}
	prompt := ""
	if len(args) > 1 {
		prompt = strings.ToLower(args[1])
	}
	value := os.Getenv(askPassPassword)
	if strings.Contains(prompt, "username") {
		value = os.Getenv(askPassUsername)
	}
	_, _ = fmt.Fprintln(out, value)
	return true
}

// Run executes Git with bounded output so a noisy remote cannot exhaust memory while building a useful failure.
func (execProcessRunner) Run(ctx context.Context, invocation processInvocation) (processResult, error) {
	stdout := newBoundedBuffer(maxCommandOutput)
	stderr := newBoundedBuffer(maxCommandOutput)
	cmd := exec.CommandContext(ctx, "git", invocation.args...)
	cmd.Env = invocation.env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	return processResult{stdout: stdout.Bytes(), stderr: stderr.Bytes()}, err
}

// Error returns only sanitized command context suitable for reports and terminal output.
func (e *commandError) Error() string {
	if e.detail == "" {
		return e.operation + " failed"
	}
	return e.operation + " failed: " + e.detail
}

// Unwrap preserves context cancellation checks without exposing the raw process error text.
func (e *commandError) Unwrap() error {
	return e.cause
}

// newBoundedBuffer creates a writer that retains only the configured diagnostic prefix.
func newBoundedBuffer(limit int) *boundedBuffer {
	return &boundedBuffer{remaining: limit}
}

// Write retains a bounded prefix because Git output can contain arbitrarily large remote diagnostics.
func (b *boundedBuffer) Write(p []byte) (int, error) {
	originalLength := len(p)
	if len(p) > b.remaining {
		p = p[:b.remaining]
		b.truncated = true
	}
	if len(p) > 0 {
		_, _ = b.data.Write(p)
		b.remaining -= len(p)
	}
	return originalLength, nil
}

// Bytes returns retained diagnostics and marks truncation so users know the remote emitted more output.
func (b *boundedBuffer) Bytes() []byte {
	data := append([]byte(nil), b.data.Bytes()...)
	if b.truncated {
		data = append(data, []byte("\n[output truncated]")...)
	}
	return data
}

// run creates an isolated hook directory and operation environment before invoking Git.
func (m *Module) run(ctx context.Context, operation string, remote Remote, args ...string) (processResult, error) {
	hooksDir, err := os.MkdirTemp("", "forge-rescue-git-hooks-")
	if err != nil {
		return processResult{}, fmt.Errorf("prepare Git operation: %w", err)
	}
	defer os.RemoveAll(hooksDir)

	invocation, err := newInvocation(remote, hooksDir, args)
	if err != nil {
		return processResult{}, err
	}
	result, runErr := m.runner.Run(ctx, invocation)
	if runErr == nil {
		return result, nil
	}
	return processResult{}, sanitizedCommandError(ctx, operation, result, runErr, invocation.secrets)
}

// newInvocation disables prompts and hooks while keeping credentials out of Git's process arguments.
func newInvocation(remote Remote, hooksDir string, args []string) (processInvocation, error) {
	environment := filteredEnvironment(os.Environ(), askPassMarker, askPassUsername, askPassPassword, "GIT_ASKPASS", "SSH_ASKPASS", "GIT_TERMINAL_PROMPT")
	environment = append(environment, "GIT_TERMINAL_PROMPT=0")
	invocation := processInvocation{
		args: append([]string{"-c", "core.hooksPath=" + hooksDir}, args...),
		env:  environment,
	}
	if remote.credential == nil {
		return invocation, nil
	}
	invocation.args = append([]string{"-c", "credential.helper="}, invocation.args...) // Explicit credentials must not be replaced by a machine-wide credential helper.
	executable, err := os.Executable()
	if err != nil {
		return processInvocation{}, fmt.Errorf("locate Git askpass executable: %w", err)
	}
	invocation.env = append(invocation.env,
		"GIT_ASKPASS="+executable,
		"SSH_ASKPASS="+executable,
		askPassMarker+"=1",
		askPassUsername+"="+remote.credential.username,
		askPassPassword+"="+remote.credential.password,
	)
	invocation.secrets = []string{remote.credential.username, remote.credential.password}
	return invocation, nil
}

// filteredEnvironment removes inherited operation variables so stale credentials cannot cross the module's seam.
func filteredEnvironment(environment []string, keys ...string) []string {
	blocked := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		blocked[strings.ToUpper(key)] = struct{}{}
	}
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if _, found := blocked[strings.ToUpper(key)]; !found {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

// sanitizedCommandError combines bounded diagnostics with an exit code while preserving cancellation identity.
func sanitizedCommandError(ctx context.Context, operation string, result processResult, runErr error, secrets []string) error {
	detail := strings.TrimSpace(string(result.stderr))
	if detail == "" {
		detail = strings.TrimSpace(string(result.stdout))
	}
	detail = redact(detail, secrets)
	if exitErr := new(exec.ExitError); errors.As(runErr, &exitErr) {
		exit := "exit code " + strconv.Itoa(exitErr.ExitCode())
		if detail == "" {
			detail = exit
		} else {
			detail = exit + ": " + detail
		}
	}
	var cause error
	if ctxErr := ctx.Err(); ctxErr != nil {
		cause = ctxErr // Context identity is safe to expose and lets callers distinguish cancellation from Git failure.
	}
	return &commandError{operation: operation, detail: detail, cause: cause}
}

// redact removes every nonempty credential from diagnostics before they can reach reports or callers.
func redact(value string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	return value
}
