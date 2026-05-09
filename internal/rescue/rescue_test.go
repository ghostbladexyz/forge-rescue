package rescue

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

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

func TestRunWritesManifestAndUsesSafeMirrorDirectoryNames(t *testing.T) {
	tmp := t.TempDir()
	scan := Scan{
		Instance:  "https://git.example",
		ScannedAt: time.Date(2026, 5, 9, 20, 0, 0, 0, time.UTC),
		Repos:     []Repo{{FullName: "team/legacy.tool", CloneURL: "https://git.example/team/legacy.tool.git"}},
	}
	if err := WriteScan(filepath.Join(tmp, "scan.json"), scan); err != nil {
		t.Fatalf("WriteScan returned error: %v", err)
	}

	runner := &recordingRunner{}
	exporter := &recordingExporter{}
	err := Run(context.Background(), Options{
		DataDir:   tmp,
		Selection: Selection{Names: []string{"team/legacy.tool"}},
		Now: func() time.Time {
			return time.Date(2026, 5, 9, 21, 0, 0, 0, time.UTC)
		},
		CommandRunner:    runner,
		MetadataExporter: exporter,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	wantDir := filepath.Join(tmp, "repos", "team-legacy.tool.git")
	if len(runner.calls) != 1 {
		t.Fatalf("git calls = %d, want 1", len(runner.calls))
	}
	if runner.calls[0].args[len(runner.calls[0].args)-1] != wantDir {
		t.Fatalf("mirror dir = %q, want %q", runner.calls[0].args[len(runner.calls[0].args)-1], wantDir)
	}

	manifest, err := ReadManifest(filepath.Join(tmp, "manifest.json"))
	if err != nil {
		t.Fatalf("ReadManifest returned error: %v", err)
	}
	if manifest.Success != 1 || manifest.Failed != 0 {
		t.Fatalf("manifest success/failed = %d/%d, want 1/0", manifest.Success, manifest.Failed)
	}
	if len(exporter.repos) != 1 || exporter.repos[0] != "team/legacy.tool" {
		t.Fatalf("exported repos = %#v, want team/legacy.tool", exporter.repos)
	}
}

type recordingRunner struct {
	calls []commandCall
}

type commandCall struct {
	name string
	args []string
}

func (r *recordingRunner) Run(ctx context.Context, name string, args ...string) error {
	r.calls = append(r.calls, commandCall{name: name, args: args})
	return nil
}

type recordingExporter struct {
	repos []string
}

func (e *recordingExporter) ExportMetadata(ctx context.Context, repo Repo, metadataDir string) error {
	e.repos = append(e.repos, repo.FullName)
	return nil
}
