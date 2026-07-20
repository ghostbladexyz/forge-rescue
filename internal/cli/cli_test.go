package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ghostbladexyz/forge-rescue/internal/github"
	"github.com/ghostbladexyz/forge-rescue/internal/gitmirror"
	"github.com/ghostbladexyz/forge-rescue/internal/rescue"
)

// TestScanCommandWritesScanFile verifies CLI scan composition persists the discovered source catalog.
func TestScanCommandWritesScanFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/user":
			writeJSON(t, w, map[string]any{"login": "alice"})
		case "/api/v1/user/orgs":
			writeJSON(t, w, []map[string]any{})
		case "/api/v1/user/repos":
			if r.URL.Query().Get("page") == "1" {
				writeJSON(t, w, []map[string]any{{"full_name": "alice/app", "clone_url": "https://git.example/alice/app.git", "updated_at": "2026-05-01T00:00:00Z"}})
				return
			}
			writeJSON(t, w, []map[string]any{})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	tmp := t.TempDir()
	var out bytes.Buffer
	err := Run(context.Background(), []string{"scan", "--instance", server.URL, "--data-dir", tmp}, Env{Token: "token"}, &out)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmp, "scan.json"))
	if err != nil {
		t.Fatalf("reading scan: %v", err)
	}
	var scan rescue.Scan
	if err := json.Unmarshal(data, &scan); err != nil {
		t.Fatalf("decoding scan: %v", err)
	}
	if scan.Instance != server.URL || len(scan.Repos) != 1 || scan.Repos[0].FullName != "alice/app" {
		t.Fatalf("scan = %#v, want one alice/app repo for server instance", scan)
	}
}

// TestRescueCommandSelectsHighRisk verifies the high-risk flag reaches rescue selection.
func TestRescueCommandSelectsHighRisk(t *testing.T) {
	tmp := t.TempDir()
	old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	scan := rescue.Scan{
		Instance: "https://git.example",
		Repos: []rescue.Repo{
			{FullName: "alice/old", CloneURL: "https://git.example/alice/old.git", UpdatedAt: old},
		},
	}
	if err := rescue.WriteScan(filepath.Join(tmp, "scan.json"), scan); err != nil {
		t.Fatalf("WriteScan returned error: %v", err)
	}

	var out bytes.Buffer
	err := Run(context.Background(), []string{"rescue", "--high-risk", "--data-dir", tmp}, Env{
		Token: "token",
		Now: func() time.Time {
			return time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)
		},
		GitMirrors:     &recordingMirrors{},
		MetadataSource: &recordingMetadataSource{},
	}, &out)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	manifest, err := rescue.ReadManifest(filepath.Join(tmp, "manifest.json"))
	if err != nil {
		t.Fatalf("ReadManifest returned error: %v", err)
	}
	if manifest.Success != 1 {
		t.Fatalf("manifest success = %d, want 1", manifest.Success)
	}
}

// TestRescueCommandSelectsMediumRisk verifies the medium-risk flag excludes high-risk repositories.
func TestRescueCommandSelectsMediumRisk(t *testing.T) {
	tmp := t.TempDir()
	now := time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)
	scan := rescue.Scan{
		Instance: "https://git.example",
		Repos: []rescue.Repo{
			{FullName: "alice/medium", CloneURL: "https://git.example/alice/medium.git", CreatedAt: now.AddDate(0, 0, -240)},
			{FullName: "alice/high", CloneURL: "https://git.example/alice/high.git", CreatedAt: now.AddDate(0, 0, -500)},
		},
	}
	if err := rescue.WriteScan(filepath.Join(tmp, "scan.json"), scan); err != nil {
		t.Fatalf("WriteScan returned error: %v", err)
	}

	var out bytes.Buffer
	err := Run(context.Background(), []string{"rescue", "--medium-risk", "--data-dir", tmp}, Env{
		Token: "token",
		Now: func() time.Time {
			return now
		},
		GitMirrors:     &recordingMirrors{},
		MetadataSource: &recordingMetadataSource{},
	}, &out)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	manifest, err := rescue.ReadManifest(filepath.Join(tmp, "manifest.json"))
	if err != nil {
		t.Fatalf("ReadManifest returned error: %v", err)
	}
	if manifest.Success != 1 {
		t.Fatalf("manifest success = %d, want 1", manifest.Success)
	}
}

// TestUploadGitHubCommandBuildsWorkflowRequest verifies CLI parsing reaches the deep destination without reproducing upload policy.
func TestUploadGitHubCommandBuildsWorkflowRequest(t *testing.T) {
	tmp := t.TempDir()
	scan := rescue.Scan{
		Repos: []rescue.Repo{{FullName: "alice/project", CloneURL: "https://git.example/alice/project.git"}},
	}
	if err := rescue.WriteScan(filepath.Join(tmp, "scan.json"), scan); err != nil {
		t.Fatalf("WriteScan returned error: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "repos", "alice-project.git"), 0o755); err != nil {
		t.Fatalf("creating mirror dir: %v", err)
	}

	destination := &recordingGitHubDestination{}
	var out bytes.Buffer
	err := Run(context.Background(), []string{"upload", "github", "--owner", "ghostbladexyz", "--replace-existing-refs", "--data-dir", tmp}, Env{
		GitHubToken: "gh-token",
		GitHub:      destination,
	}, &out)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if len(destination.uploads) != 1 || destination.uploads[0].Owner != "ghostbladexyz" || destination.uploads[0].Existing != github.ReplaceExistingRefs || destination.uploads[0].Workspace == nil {
		t.Fatalf("upload requests = %#v, want one replacement workflow", destination.uploads)
	}
}

// TestUploadGitHubCommandKeepsDeprecatedForceAlias verifies compatibility maps the old flag to the explicit replacement policy.
func TestUploadGitHubCommandKeepsDeprecatedForceAlias(t *testing.T) {
	destination := &recordingGitHubDestination{}
	var out bytes.Buffer
	err := Run(context.Background(), []string{"upload", "github", "--owner", "alice", "--force-existing", "--data-dir", t.TempDir()}, Env{
		GitHubToken: "token",
		GitHub:      destination,
	}, &out)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(destination.uploads) != 1 || destination.uploads[0].Existing != github.ReplaceExistingRefs || !strings.Contains(out.String(), "deprecated") {
		t.Fatalf("force alias request/output = %#v/%q", destination.uploads, out.String())
	}
}

// TestDeleteGitHubCommandPassesConfirmedExactNames verifies CLI confirmation does not transform destination names.
func TestDeleteGitHubCommandPassesConfirmedExactNames(t *testing.T) {
	destination := &recordingGitHubDestination{}
	var out bytes.Buffer
	err := Run(context.Background(), []string{"delete", "github", "--owner", "ghostbladexyz", "--confirm-delete", "ghostbladexyz", "alice-project", "bob-tool"}, Env{
		GitHubToken: "gh-token",
		GitHub:      destination,
	}, &out)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if len(destination.deletes) != 1 || destination.deletes[0].Owner != "ghostbladexyz" || !reflect.DeepEqual(destination.deletes[0].Repositories, []string{"alice-project", "bob-tool"}) {
		t.Fatalf("delete requests = %#v", destination.deletes)
	}
}

// TestDeleteGitHubCommandRequiresMatchingConfirmation verifies irreversible requests never reach the destination on a typo.
func TestDeleteGitHubCommandRequiresMatchingConfirmation(t *testing.T) {
	destination := &recordingGitHubDestination{}
	err := Run(context.Background(), []string{"delete", "github", "--owner", "alice", "--confirm-delete", "Alice", "project"}, Env{
		GitHubToken: "token",
		GitHub:      destination,
	}, io.Discard)
	if err == nil || len(destination.deletes) != 0 {
		t.Fatalf("Run error/deletes = %v/%#v, want confirmation failure before mutation", err, destination.deletes)
	}
}

// writeJSON emits deterministic HTTP fixtures for CLI integration tests.
func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encoding json: %v", err)
	}
}

type recordingMirrors struct{}

// Clone creates the requested artifact because CLI tests verify orchestration rather than Git behavior.
func (recordingMirrors) Clone(ctx context.Context, source gitmirror.Remote, destination string) error {
	return os.MkdirAll(destination, 0o700)
}

// Push accepts a validated workflow request because transport behavior is covered by gitmirror tests.
func (recordingMirrors) Push(ctx context.Context, mirrorPath string, destination gitmirror.Remote) error {
	return nil
}

type recordingMetadataSource struct{}

// CaptureMetadata returns a complete archive because CLI tests exercise orchestration rather than remote capture details.
func (recordingMetadataSource) CaptureMetadata(ctx context.Context, repo rescue.Repo) (rescue.RepositoryMetadata, error) {
	return rescue.RepositoryMetadata{
		Repository: json.RawMessage(`{"full_name":"` + repo.FullName + `"}`),
		Issues:     []json.RawMessage{},
		Releases:   []json.RawMessage{},
		Labels:     []json.RawMessage{},
	}, nil
}

type recordingGitHubDestination struct {
	uploads []github.UploadRequest
	deletes []github.DeleteRequest
}

// Upload records the workflow-level request because GitHub policy is tested inside its owning module.
func (d *recordingGitHubDestination) Upload(ctx context.Context, request github.UploadRequest) (github.UploadReport, error) {
	d.uploads = append(d.uploads, request)
	return github.UploadReport{}, nil
}

// Delete records exact names after CLI confirmation while deletion behavior remains tested inside the GitHub module.
func (d *recordingGitHubDestination) Delete(ctx context.Context, request github.DeleteRequest) (github.DeleteReport, error) {
	d.deletes = append(d.deletes, request)
	return github.DeleteReport{}, nil
}
