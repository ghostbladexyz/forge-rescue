package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ghostbladexyz/forge-rescue/internal/rescue"
)

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
		CommandRunner:    &recordingRunner{},
		MetadataExporter: &recordingExporter{},
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
		CommandRunner:    &recordingRunner{},
		MetadataExporter: &recordingExporter{},
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

func TestUploadGitHubCommandUsesGitHubTokenAndOwner(t *testing.T) {
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

	var out bytes.Buffer
	err := Run(context.Background(), []string{"upload", "github", "--owner", "ghostbladexyz", "--data-dir", tmp}, Env{
		GitHubToken:      "gh-token",
		GitHubClient:     &recordingGitHub{},
		CommandRunner:    &recordingRunner{},
		MetadataExporter: &recordingExporter{},
		Now: func() time.Time {
			return time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)
		},
	}, &out)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	report, err := os.ReadFile(filepath.Join(tmp, "upload-github.json"))
	if err != nil {
		t.Fatalf("reading report: %v", err)
	}
	if !bytes.Contains(report, []byte(`"success": 1`)) {
		t.Fatalf("report = %s, want success 1", report)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encoding json: %v", err)
	}
}

type recordingRunner struct{}

func (recordingRunner) Run(ctx context.Context, name string, args ...string) error {
	return nil
}

type recordingExporter struct{}

func (recordingExporter) ExportMetadata(ctx context.Context, repo rescue.Repo, metadataDir string) error {
	return nil
}

type recordingGitHub struct {
	repos map[string]bool
}

func (g *recordingGitHub) RepositoryExists(ctx context.Context, owner, name string) (bool, error) {
	return g.repos[owner+"/"+name], nil
}

func (g *recordingGitHub) CreateRepository(ctx context.Context, owner, name string, private bool) error {
	if !private {
		return nil
	}
	if g.repos == nil {
		g.repos = map[string]bool{}
	}
	g.repos[owner+"/"+name] = true
	return nil
}

func (g *recordingGitHub) HasRefs(ctx context.Context, owner, name string) (bool, error) {
	return false, nil
}
