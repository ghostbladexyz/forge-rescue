package upload

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ghostbladexyz/forge-rescue/internal/rescue"
)

type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
}

type GitHubClient interface {
	RepositoryExists(ctx context.Context, owner, name string) (bool, error)
	CreateRepository(ctx context.Context, owner, name string, private bool) error
	HasRefs(ctx context.Context, owner, name string) (bool, error)
}

type Options struct {
	DataDir       string
	Owner         string
	Token         string
	ForceExisting bool
	GitHub        GitHubClient
	CommandRunner CommandRunner
	Now           func() time.Time
}

func Run(ctx context.Context, opts Options) error {
	if opts.DataDir == "" {
		opts.DataDir = "forge-rescue-data"
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Owner == "" {
		return fmt.Errorf("upload requires --owner")
	}
	if opts.Token == "" {
		return fmt.Errorf("set GITHUB_TOKEN or provide a GitHub token in the environment")
	}
	if opts.GitHub == nil {
		return fmt.Errorf("missing GitHub client")
	}
	if opts.CommandRunner == nil {
		opts.CommandRunner = rescue.ExecRunner{}
	}

	scan, err := rescue.ReadScan(filepath.Join(opts.DataDir, "scan.json"))
	if err != nil {
		return fmt.Errorf("read scan: %w", err)
	}
	repos := reposWithLocalMirrors(opts.DataDir, scan.Repos)

	report := Report{
		Provider:   "github",
		Owner:      opts.Owner,
		UploadedAt: opts.Now().UTC(),
		ReposTotal: len(repos),
	}
	for _, repo := range repos {
		if err := uploadRepo(ctx, opts, repo); err != nil {
			if isSkipped(err) {
				report.Skipped++
				report.Failures = append(report.Failures, Failure{Repo: repo.FullName, Error: err.Error()})
				continue
			}
			report.Failed++
			report.Failures = append(report.Failures, Failure{Repo: repo.FullName, Error: err.Error()})
			continue
		}
		report.Success++
	}

	if err := WriteReport(filepath.Join(opts.DataDir, "upload-github.json"), report); err != nil {
		return fmt.Errorf("write upload report: %w", err)
	}
	if report.Failed > 0 {
		return fmt.Errorf("uploaded %d repos with %d failures and %d skips", report.Success, report.Failed, report.Skipped)
	}
	return nil
}

func uploadRepo(ctx context.Context, opts Options, repo rescue.Repo) error {
	name := rescue.SafeName(repo.FullName)
	mirror := filepath.Join(opts.DataDir, "repos", rescue.MirrorDir(repo.FullName))
	if _, err := os.Stat(mirror); err != nil {
		return fmt.Errorf("local mirror missing: %w", err)
	}

	exists, err := opts.GitHub.RepositoryExists(ctx, opts.Owner, name)
	if err != nil {
		return err
	}
	if !exists {
		if err := opts.GitHub.CreateRepository(ctx, opts.Owner, name, true); err != nil {
			return err
		}
	} else if !opts.ForceExisting {
		hasRefs, err := opts.GitHub.HasRefs(ctx, opts.Owner, name)
		if err != nil {
			return err
		}
		if hasRefs {
			return skipError{message: "github repo already exists and is not empty; use --force-existing to push anyway"}
		}
	}

	remote := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", opts.Token, opts.Owner, name)
	return opts.CommandRunner.Run(ctx, "git", "-C", mirror, "push", "--mirror", remote)
}

func reposWithLocalMirrors(dataDir string, repos []rescue.Repo) []rescue.Repo {
	var selected []rescue.Repo
	for _, repo := range repos {
		mirror := filepath.Join(dataDir, "repos", rescue.MirrorDir(repo.FullName))
		if info, err := os.Stat(mirror); err == nil && info.IsDir() {
			selected = append(selected, repo)
		}
	}
	return selected
}

type skipError struct {
	message string
}

func (e skipError) Error() string {
	return e.message
}

func isSkipped(err error) bool {
	_, ok := err.(skipError)
	return ok
}
