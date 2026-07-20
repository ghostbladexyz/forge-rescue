package rescue

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ghostbladexyz/forge-rescue/internal/gitmirror"
)

// MirrorCloner is the workflow seam for storing a remote in a workspace-owned artifact path.
type MirrorCloner interface {
	Clone(ctx context.Context, source gitmirror.Remote, destination string) error
}

// metadataCapturer is an internal workflow seam that keeps remote capture independent from workspace paths.
type metadataCapturer interface {
	CaptureMetadata(ctx context.Context, repo Repo) (RepositoryMetadata, error)
}

type Options struct {
	DataDir        string
	Selection      Selection
	RiskConfig     RiskConfig
	Now            func() time.Time
	GitMirrors     MirrorCloner
	MetadataSource metadataCapturer
}

// SelectRepos applies explicit-name selection first and otherwise filters by the requested risk level.
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

// Run preserves the path-based entry point while delegating workspace rules to the deep Workspace module.
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
	if opts.GitMirrors == nil {
		opts.GitMirrors = gitmirror.New()
	}
	if opts.MetadataSource == nil {
		return errors.New("metadata source is required")
	}

	workspace, err := OpenWorkspace(opts.DataDir)
	if err != nil {
		return fmt.Errorf("open workspace: %w", err)
	}
	scan, err := workspace.LoadScan()
	if err != nil {
		return fmt.Errorf("read scan: %w", err)
	}

	runTime := opts.Now()
	selected, err := workspace.Select(scan, opts.Selection, opts.RiskConfig, runTime)
	if err != nil {
		return err
	}
	if err := workspace.ensureIndex(scan, true); err != nil {
		return fmt.Errorf("prepare workspace index: %w", err)
	}
	if err := workspace.ensurePrivateDirectories(); err != nil {
		return err
	}

	manifest := Manifest{
		Instance:   scan.Instance,
		RescuedAt:  runTime.UTC(),
		ReposTotal: len(selected),
	}
	for _, repo := range selected {
		artifact, err := workspace.artifact(repo)
		if err != nil {
			return err
		}
		outcome := Outcome{Repo: repo.FullName, Identity: artifact.Identity, ArtifactKey: artifact.Key}

		if !artifact.MirrorComplete || !directoryExists(artifact.MirrorPath) {
			if err := os.MkdirAll(filepath.Dir(artifact.MirrorPath), 0o700); err != nil {
				return err
			}
			source, err := gitmirror.NewRemote(repo.CloneURL)
			if err == nil {
				err = opts.GitMirrors.Clone(ctx, source, artifact.MirrorPath)
			}
			if err != nil {
				outcome.Status = OutcomeFailed
				outcome.Error = err.Error()
				manifest.Failed++
				manifest.Failures = append(manifest.Failures, Failure{Repo: repo.FullName, Error: err.Error()})
				manifest.Outcomes = append(manifest.Outcomes, outcome)
				continue
			}
			if err := workspace.markPhase(artifact.Identity, true, false); err != nil {
				return fmt.Errorf("record mirror completion for %s: %w", repo.FullName, err)
			}
			artifact.MirrorComplete = true
		}
		outcome.MirrorComplete = true

		if !artifact.MetadataComplete {
			metadata, err := opts.MetadataSource.CaptureMetadata(ctx, repo)
			if err == nil {
				err = workspace.SaveMetadata(repo, metadata)
			}
			if err != nil {
				outcome.Status = OutcomePartial
				outcome.Error = err.Error()
				manifest.Failed++
				manifest.Failures = append(manifest.Failures, Failure{Repo: repo.FullName, Error: err.Error()})
				manifest.Outcomes = append(manifest.Outcomes, outcome)
				continue
			}
		}
		if err := workspace.markPhase(artifact.Identity, true, true); err != nil {
			return fmt.Errorf("record rescue completion for %s: %w", repo.FullName, err)
		}
		outcome.MetadataComplete = true
		outcome.Status = OutcomeComplete
		manifest.Success++
		manifest.Outcomes = append(manifest.Outcomes, outcome)
	}

	if err := workspace.SaveManifest(manifest); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	if manifest.Failed > 0 {
		return fmt.Errorf("rescued %d repos with %d failures", manifest.Success, manifest.Failed)
	}
	return nil
}

// MirrorDir returns the flattened mirror name used only when reading a legacy workspace.
func MirrorDir(fullName string) string {
	return SafeName(fullName) + ".git"
}

// SafeName returns the historical flattened name retained only for legacy workspace compatibility.
func SafeName(fullName string) string {
	safe := strings.ReplaceAll(fullName, "/", "-")
	return safe
}
