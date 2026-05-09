package upload

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ghostbladexyz/forge-rescue/internal/rescue"
)

func TestRunCreatesPrivateRepoAndPushesMirror(t *testing.T) {
	tmp := t.TempDir()
	scan := rescue.Scan{
		Repos: []rescue.Repo{{FullName: "alice/project", CloneURL: "https://git.example/alice/project.git"}},
	}
	if err := rescue.WriteScan(filepath.Join(tmp, "scan.json"), scan); err != nil {
		t.Fatalf("WriteScan returned error: %v", err)
	}
	mirror := filepath.Join(tmp, "repos", "alice-project.git")
	if err := mkdir(mirror); err != nil {
		t.Fatalf("creating mirror dir: %v", err)
	}

	gh := &recordingGitHub{}
	runner := &recordingRunner{}
	err := Run(context.Background(), Options{
		DataDir:       tmp,
		Owner:         "ghostbladexyz",
		Token:         "gh-token",
		GitHub:        gh,
		CommandRunner: runner,
		Now:           fixedNow,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if len(gh.created) != 1 || gh.created[0].name != "alice-project" || !gh.created[0].private {
		t.Fatalf("created repos = %#v, want private alice-project", gh.created)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("git calls = %d, want 1", len(runner.calls))
	}
	gotArgs := runner.calls[0].args
	if gotArgs[0] != "-C" || gotArgs[1] != mirror || gotArgs[2] != "push" || gotArgs[3] != "--mirror" {
		t.Fatalf("git args = %#v, want -C mirror push --mirror remote", gotArgs)
	}

	report, err := ReadReport(filepath.Join(tmp, "upload-github.json"))
	if err != nil {
		t.Fatalf("ReadReport returned error: %v", err)
	}
	if report.Success != 1 || report.Failed != 0 || report.Skipped != 0 {
		t.Fatalf("report success/failed/skipped = %d/%d/%d, want 1/0/0", report.Success, report.Failed, report.Skipped)
	}
}

func TestRunSkipsExistingRepoWithRefsUnlessForced(t *testing.T) {
	tmp := t.TempDir()
	scan := rescue.Scan{
		Repos: []rescue.Repo{{FullName: "alice/project", CloneURL: "https://git.example/alice/project.git"}},
	}
	if err := rescue.WriteScan(filepath.Join(tmp, "scan.json"), scan); err != nil {
		t.Fatalf("WriteScan returned error: %v", err)
	}
	if err := mkdir(filepath.Join(tmp, "repos", "alice-project.git")); err != nil {
		t.Fatalf("creating mirror dir: %v", err)
	}

	gh := &recordingGitHub{repos: map[string]bool{"ghostbladexyz/alice-project": true}, refs: map[string]bool{"ghostbladexyz/alice-project": true}}
	runner := &recordingRunner{}
	err := Run(context.Background(), Options{
		DataDir:       tmp,
		Owner:         "ghostbladexyz",
		Token:         "gh-token",
		GitHub:        gh,
		CommandRunner: runner,
		Now:           fixedNow,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if len(runner.calls) != 0 {
		t.Fatalf("git calls = %d, want 0", len(runner.calls))
	}
	report, err := ReadReport(filepath.Join(tmp, "upload-github.json"))
	if err != nil {
		t.Fatalf("ReadReport returned error: %v", err)
	}
	if report.Skipped != 1 || report.Success != 0 {
		t.Fatalf("report skipped/success = %d/%d, want 1/0", report.Skipped, report.Success)
	}
}

func TestRunOnlyUploadsReposWithLocalMirrors(t *testing.T) {
	tmp := t.TempDir()
	scan := rescue.Scan{
		Repos: []rescue.Repo{
			{FullName: "alice/rescued", CloneURL: "https://git.example/alice/rescued.git"},
			{FullName: "alice/not-rescued", CloneURL: "https://git.example/alice/not-rescued.git"},
		},
	}
	if err := rescue.WriteScan(filepath.Join(tmp, "scan.json"), scan); err != nil {
		t.Fatalf("WriteScan returned error: %v", err)
	}
	if err := mkdir(filepath.Join(tmp, "repos", "alice-rescued.git")); err != nil {
		t.Fatalf("creating mirror dir: %v", err)
	}

	runner := &recordingRunner{}
	err := Run(context.Background(), Options{
		DataDir:       tmp,
		Owner:         "ghostbladexyz",
		Token:         "gh-token",
		GitHub:        &recordingGitHub{},
		CommandRunner: runner,
		Now:           fixedNow,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if len(runner.calls) != 1 {
		t.Fatalf("git calls = %d, want 1", len(runner.calls))
	}
	report, err := ReadReport(filepath.Join(tmp, "upload-github.json"))
	if err != nil {
		t.Fatalf("ReadReport returned error: %v", err)
	}
	if report.ReposTotal != 1 || report.Success != 1 || report.Failed != 0 {
		t.Fatalf("report total/success/failed = %d/%d/%d, want 1/1/0", report.ReposTotal, report.Success, report.Failed)
	}
}

func fixedNow() time.Time {
	return time.Date(2026, 5, 9, 20, 0, 0, 0, time.UTC)
}
