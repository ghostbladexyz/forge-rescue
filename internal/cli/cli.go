package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/ghostbladexyz/forge-rescue/internal/gitea"
	"github.com/ghostbladexyz/forge-rescue/internal/github"
	"github.com/ghostbladexyz/forge-rescue/internal/gitmirror"
	"github.com/ghostbladexyz/forge-rescue/internal/rescue"
)

type Env struct {
	Token          string
	GitHubToken    string
	Now            func() time.Time
	GitMirrors     gitMirrorOperations
	MetadataSource metadataCapturer
	GitHub         githubDestination
}

type gitMirrorOperations interface {
	rescue.MirrorCloner
}

type metadataCapturer interface {
	CaptureMetadata(ctx context.Context, repo rescue.Repo) (rescue.RepositoryMetadata, error)
}

type githubDestination interface {
	Upload(ctx context.Context, request github.UploadRequest) (github.UploadReport, error)
	Delete(ctx context.Context, request github.DeleteRequest) (github.DeleteReport, error)
}

// Run dispatches one CLI command after applying injectable defaults shared by every workflow.
func Run(ctx context.Context, args []string, env Env, out io.Writer) error {
	if len(args) == 0 {
		return usage(out)
	}
	if env.Token == "" {
		env.Token = os.Getenv("FORGE_RESCUE_TOKEN")
	}
	if env.Now == nil {
		env.Now = time.Now
	}
	if env.GitMirrors == nil {
		env.GitMirrors = gitmirror.New()
	}

	switch args[0] {
	case "scan":
		return runScan(ctx, args[1:], env, out)
	case "rescue":
		return runRescue(ctx, args[1:], env, out)
	case "upload":
		return runUpload(ctx, args[1:], env, out)
	case "delete":
		return runDelete(ctx, args[1:], env, out)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// runScan captures the accessible source repositories into the selected rescue workspace.
func runScan(ctx context.Context, args []string, env Env, out io.Writer) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	instance := fs.String("instance", "", "Gitea instance URL")
	dataDir := fs.String("data-dir", "forge-rescue-data", "output directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *instance == "" {
		return fmt.Errorf("scan requires --instance")
	}
	if env.Token == "" {
		return fmt.Errorf("set FORGE_RESCUE_TOKEN or provide a token in the environment")
	}

	source, err := gitea.NewSource(*instance, env.Token)
	if err != nil {
		return err
	}
	repos, err := source.Discover(ctx)
	if err != nil {
		return err
	}
	scan := rescue.Scan{Instance: *instance, ScannedAt: env.Now().UTC(), Repos: repos}
	workspace, err := rescue.OpenWorkspace(*dataDir)
	if err != nil {
		return err
	}
	if err := workspace.SaveScan(scan); err != nil {
		return err
	}
	fmt.Fprintf(out, "Found %d repositories\n", len(repos))
	printRiskSummary(out, repos, env.Now())
	return nil
}

// runRescue selects scanned repositories and delegates artifact paths and Git transport to their owning modules.
func runRescue(ctx context.Context, args []string, env Env, out io.Writer) error {
	fs := flag.NewFlagSet("rescue", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	highRisk := fs.Bool("high-risk", false, "rescue only high-risk repositories")
	mediumRisk := fs.Bool("medium-risk", false, "rescue only medium-risk repositories")
	dataDir := fs.String("data-dir", "forge-rescue-data", "output directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	selection := rescue.Selection{Names: fs.Args()}
	if *highRisk && *mediumRisk {
		return fmt.Errorf("choose only one risk flag")
	}
	if *highRisk {
		selection.Risk = rescue.RiskHigh
	}
	if *mediumRisk {
		selection.Risk = rescue.RiskMedium
	}

	metadataSource := env.MetadataSource
	if metadataSource == nil {
		if env.Token == "" {
			env.Token = os.Getenv("FORGE_RESCUE_TOKEN")
		}
		workspace, err := rescue.OpenWorkspace(*dataDir)
		if err != nil {
			return err
		}
		scan, err := workspace.LoadScan()
		if err != nil {
			return err
		}
		metadataSource, err = gitea.NewSource(scan.Instance, env.Token)
		if err != nil {
			return err
		}
	}

	err := rescue.Run(ctx, rescue.Options{
		DataDir:        *dataDir,
		Selection:      selection,
		Now:            env.Now,
		GitMirrors:     env.GitMirrors,
		MetadataSource: metadataSource,
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "Rescue complete")
	return nil
}

// runUpload validates GitHub upload arguments before delegating destination and mirror behavior.
func runUpload(ctx context.Context, args []string, env Env, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("upload requires a provider")
	}
	if args[0] != "github" {
		return fmt.Errorf("unsupported upload provider %q", args[0])
	}

	fs := flag.NewFlagSet("upload github", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	owner := fs.String("owner", "", "GitHub user or organization that will receive repositories")
	dataDir := fs.String("data-dir", "forge-rescue-data", "output directory")
	replaceExisting := fs.Bool("replace-existing-refs", false, "replace all refs in existing non-empty GitHub repositories")
	forceExisting := fs.Bool("force-existing", false, "deprecated alias for --replace-existing-refs")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if env.GitHubToken == "" {
		env.GitHubToken = os.Getenv("GITHUB_TOKEN")
	}
	workspace, err := rescue.OpenWorkspace(*dataDir)
	if err != nil {
		return err
	}
	destination := env.GitHub
	if destination == nil {
		destination = github.New(env.GitHubToken)
	}
	policy := github.CreateOrFillEmpty
	if *replaceExisting || *forceExisting {
		policy = github.ReplaceExistingRefs
	}
	if *forceExisting {
		fmt.Fprintln(out, "Warning: --force-existing is deprecated; use --replace-existing-refs")
	}
	_, err = destination.Upload(ctx, github.UploadRequest{
		Workspace: workspace,
		Owner:     *owner,
		Existing:  policy,
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "GitHub upload complete")
	return nil
}

// runDelete requires explicit destructive intent before removing named GitHub repositories.
func runDelete(ctx context.Context, args []string, env Env, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("delete requires a provider")
	}
	if args[0] != "github" {
		return fmt.Errorf("unsupported delete provider %q", args[0])
	}

	fs := flag.NewFlagSet("delete github", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	owner := fs.String("owner", "", "GitHub user or organization that owns repositories")
	confirmDelete := fs.String("confirm-delete", "", "repeat OWNER to confirm permanent repository deletion")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *owner == "" {
		return fmt.Errorf("delete github requires --owner")
	}
	if *confirmDelete != *owner {
		return fmt.Errorf("delete github requires --confirm-delete OWNER matching --owner exactly")
	}
	names := fs.Args()
	if len(names) == 0 {
		return fmt.Errorf("delete github requires at least one repository name")
	}
	if env.GitHubToken == "" {
		env.GitHubToken = os.Getenv("GITHUB_TOKEN")
	}
	if env.GitHubToken == "" {
		return fmt.Errorf("set GITHUB_TOKEN or provide a GitHub token in the environment")
	}

	destination := env.GitHub
	if destination == nil {
		destination = github.New(env.GitHubToken)
	}
	report, err := destination.Delete(ctx, github.DeleteRequest{Owner: *owner, Repositories: names})
	for _, outcome := range report.Outcomes {
		switch outcome.Status {
		case github.DeleteRemoved:
			fmt.Fprintf(out, "Deleted %s/%s\n", *owner, outcome.Repository)
		case github.DeleteSkippedNotFound:
			fmt.Fprintf(out, "Skipped %s/%s: not found or inaccessible\n", *owner, outcome.Repository)
		}
	}
	if err != nil {
		return err
	}
	return nil
}

// printRiskSummary groups scanned repositories using the same classification rules as rescue selection.
func printRiskSummary(out io.Writer, repos []rescue.Repo, now time.Time) {
	cfg := rescue.DefaultRiskConfig()
	groups := []struct {
		title string
		risk  string
	}{
		{"HIGH RISK", rescue.RiskHigh},
		{"MEDIUM RISK", rescue.RiskMedium},
		{"SAFE", rescue.RiskSafe},
	}
	for _, group := range groups {
		fmt.Fprintln(out)
		fmt.Fprintln(out, group.title)
		fmt.Fprintln(out, "----------")
		for _, repo := range repos {
			risk := rescue.Classify(repo, cfg, now)
			if risk.Level == group.risk {
				fmt.Fprintf(out, "%s created %d days ago\n", repo.FullName, risk.AgeDays)
			}
		}
	}
}

// usage writes the compact command overview used when no command is supplied.
func usage(out io.Writer) error {
	fmt.Fprintln(out, "usage: forge-rescue scan --instance URL | forge-rescue rescue [--high-risk|--medium-risk] [owner/repo...] | forge-rescue upload github --owner OWNER | forge-rescue delete github --owner OWNER --confirm-delete OWNER repo...")
	return nil
}
