package rescue

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ghostbladexyz/forge-rescue/internal/gitmirror"
)

// TestSelectReposChoosesHighRiskFromScan verifies that risk selection remains deterministic at the rescue interface.
func TestSelectReposChoosesHighRiskFromScan(t *testing.T) {
	now := time.Date(2026, 5, 9, 20, 0, 0, 0, time.UTC)
	scan := Scan{
		Repos: []Repo{
			{FullName: "owner/old", CloneURL: "https://git.example/owner/old.git", CreatedAt: now.AddDate(0, 0, -500), PushedAt: ptrTime(now.AddDate(0, 0, -1))},
			{FullName: "owner/new", CloneURL: "https://git.example/owner/new.git", CreatedAt: now.AddDate(0, 0, -3), PushedAt: ptrTime(now.AddDate(0, 0, -3))},
		},
	}

	got := SelectRepos(scan, Selection{Risk: RiskHigh}, RiskConfig{HighDays: 365, MediumDays: 180}, now)

	if len(got) != 1 {
		t.Fatalf("selected count = %d, want 1", len(got))
	}
	if got[0].FullName != "owner/old" {
		t.Fatalf("selected repo = %q, want owner/old", got[0].FullName)
	}
}

// TestRunWritesManifestAndUsesWorkspaceArtifactNames verifies that local identity no longer depends on flattened display names.
func TestRunWritesManifestAndUsesWorkspaceArtifactNames(t *testing.T) {
	tmp := t.TempDir()
	scan := Scan{
		Instance:  "https://git.example",
		ScannedAt: time.Date(2026, 5, 9, 20, 0, 0, 0, time.UTC),
		Repos:     []Repo{{FullName: "team/legacy.tool", CloneURL: "https://git.example/team/legacy.tool.git"}},
	}
	if err := WriteScan(filepath.Join(tmp, "scan.json"), scan); err != nil {
		t.Fatalf("WriteScan returned error: %v", err)
	}

	mirrors := &recordingMirrors{}
	source := &recordingMetadataSource{}
	err := Run(context.Background(), Options{
		DataDir:   tmp,
		Selection: Selection{Names: []string{"team/legacy.tool"}},
		Now: func() time.Time {
			return time.Date(2026, 5, 9, 21, 0, 0, 0, time.UTC)
		},
		GitMirrors:     mirrors,
		MetadataSource: source,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	workspace, err := OpenWorkspace(tmp)
	if err != nil {
		t.Fatalf("OpenWorkspace returned error: %v", err)
	}
	artifact, err := workspace.ArtifactFor(scan.Repos[0])
	if err != nil {
		t.Fatalf("ArtifactFor returned error: %v", err)
	}
	wantDir := artifact.MirrorPath
	if len(mirrors.destinations) != 1 {
		t.Fatalf("mirror clones = %d, want 1", len(mirrors.destinations))
	}
	if mirrors.destinations[0] != wantDir {
		t.Fatalf("mirror dir = %q, want %q", mirrors.destinations[0], wantDir)
	}

	manifest, err := ReadManifest(filepath.Join(tmp, "manifest.json"))
	if err != nil {
		t.Fatalf("ReadManifest returned error: %v", err)
	}
	if manifest.Success != 1 || manifest.Failed != 0 {
		t.Fatalf("manifest success/failed = %d/%d, want 1/0", manifest.Success, manifest.Failed)
	}
	if len(source.repos) != 1 || source.repos[0] != "team/legacy.tool" {
		t.Fatalf("captured repos = %#v, want team/legacy.tool", source.repos)
	}
}

type recordingMirrors struct {
	destinations []string
}

// Clone records workflow intent and creates the artifact because the real module owns Git command details.
func (r *recordingMirrors) Clone(ctx context.Context, source gitmirror.Remote, destination string) error {
	r.destinations = append(r.destinations, destination)
	return os.MkdirAll(destination, 0o700)
}

type recordingMetadataSource struct {
	repos []string
}

// CaptureMetadata records repository names and returns a complete archive because Workspace rejects incomplete captures.
func (s *recordingMetadataSource) CaptureMetadata(ctx context.Context, repo Repo) (RepositoryMetadata, error) {
	s.repos = append(s.repos, repo.FullName)
	return RepositoryMetadata{
		Repository: json.RawMessage(`{"full_name":"` + repo.FullName + `"}`),
		Issues:     []json.RawMessage{},
		Releases:   []json.RawMessage{},
		Labels:     []json.RawMessage{},
	}, nil
}
