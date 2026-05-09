package rescue

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
}

type MetadataExporter interface {
	ExportMetadata(ctx context.Context, repo Repo, metadataDir string) error
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

type Options struct {
	DataDir          string
	Selection        Selection
	RiskConfig       RiskConfig
	Now              func() time.Time
	CommandRunner    CommandRunner
	MetadataExporter MetadataExporter
}

func SelectRepos(scan Scan, selection Selection, cfg RiskConfig, now time.Time) []Repo {
	if len(selection.Names) > 0 {
		wanted := make(map[string]bool, len(selection.Names))
		for _, name := range selection.Names {
			wanted[name] = true
		}
		var selected []Repo
		for _, repo := range scan.Repos {
			if wanted[repo.FullName] {
				selected = append(selected, repo)
			}
		}
		return selected
	}

	if selection.Risk == "" {
		return scan.Repos
	}

	var selected []Repo
	for _, repo := range scan.Repos {
		if Classify(repo, cfg, now).Level == selection.Risk {
			selected = append(selected, repo)
		}
	}
	return selected
}

func Run(ctx context.Context, opts Options) error {
	if opts.DataDir == "" {
		opts.DataDir = "forge-rescue-data"
	}
	if opts.RiskConfig == (RiskConfig{}) {
		opts.RiskConfig = DefaultRiskConfig()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.CommandRunner == nil {
		opts.CommandRunner = ExecRunner{}
	}

	scan, err := ReadScan(filepath.Join(opts.DataDir, "scan.json"))
	if err != nil {
		return fmt.Errorf("read scan: %w", err)
	}

	selected := SelectRepos(scan, opts.Selection, opts.RiskConfig, opts.Now())
	if err := os.MkdirAll(filepath.Join(opts.DataDir, "repos"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(opts.DataDir, "metadata"), 0o755); err != nil {
		return err
	}

	manifest := Manifest{
		Instance:   scan.Instance,
		RescuedAt:  opts.Now().UTC(),
		ReposTotal: len(selected),
	}
	for _, repo := range selected {
		target := filepath.Join(opts.DataDir, "repos", MirrorDir(repo.FullName))
		if err := opts.CommandRunner.Run(ctx, "git", "clone", "--mirror", repo.CloneURL, target); err != nil {
			manifest.Failed++
			manifest.Failures = append(manifest.Failures, Failure{Repo: repo.FullName, Error: err.Error()})
			continue
		}
		if opts.MetadataExporter != nil {
			if err := opts.MetadataExporter.ExportMetadata(ctx, repo, filepath.Join(opts.DataDir, "metadata")); err != nil {
				manifest.Failed++
				manifest.Failures = append(manifest.Failures, Failure{Repo: repo.FullName, Error: err.Error()})
				continue
			}
		}
		manifest.Success++
	}

	if err := WriteManifest(filepath.Join(opts.DataDir, "manifest.json"), manifest); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	if manifest.Failed > 0 {
		return fmt.Errorf("rescued %d repos with %d failures", manifest.Success, manifest.Failed)
	}
	return nil
}

func MirrorDir(fullName string) string {
	return SafeName(fullName) + ".git"
}

func SafeName(fullName string) string {
	safe := strings.ReplaceAll(fullName, "/", "-")
	return safe
}
