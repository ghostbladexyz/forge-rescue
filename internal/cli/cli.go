package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ghostbladexyz/forge-rescue/internal/gitea"
	"github.com/ghostbladexyz/forge-rescue/internal/github"
	"github.com/ghostbladexyz/forge-rescue/internal/rescue"
	"github.com/ghostbladexyz/forge-rescue/internal/upload"
)

type Env struct {
	Token            string
	GitHubToken      string
	Now              func() time.Time
	CommandRunner    rescue.CommandRunner
	MetadataExporter rescue.MetadataExporter
	GitHubClient     gitHubClient
}

type gitHubClient interface {
	upload.GitHubClient
	DeleteRepository(ctx context.Context, owner, name string) error
}

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

	client := gitea.NewClient(*instance, env.Token)
	repos, err := client.ListRepositories(ctx)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		return err
	}
	scan := rescue.Scan{Instance: *instance, ScannedAt: env.Now().UTC(), Repos: repos}
	if err := rescue.WriteScan(filepath.Join(*dataDir, "scan.json"), scan); err != nil {
		return err
	}
	fmt.Fprintf(out, "Found %d repositories\n", len(repos))
	printRiskSummary(out, repos, env.Now())
	return nil
}

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

	exporter := env.MetadataExporter
	if exporter == nil {
		if env.Token == "" {
			env.Token = os.Getenv("FORGE_RESCUE_TOKEN")
		}
		scan, err := rescue.ReadScan(filepath.Join(*dataDir, "scan.json"))
		if err != nil {
			return err
		}
		exporter = gitea.NewClient(scan.Instance, env.Token)
	}

	err := rescue.Run(ctx, rescue.Options{
		DataDir:          *dataDir,
		Selection:        selection,
		Now:              env.Now,
		CommandRunner:    env.CommandRunner,
		MetadataExporter: exporter,
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "Rescue complete")
	return nil
}

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
	forceExisting := fs.Bool("force-existing", false, "push into existing non-empty GitHub repositories")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if env.GitHubToken == "" {
		env.GitHubToken = os.Getenv("GITHUB_TOKEN")
	}

	client := env.GitHubClient
	if client == nil {
		client = github.NewClient(env.GitHubToken)
	}
	err := upload.Run(ctx, upload.Options{
		DataDir:       *dataDir,
		Owner:         *owner,
		Token:         env.GitHubToken,
		ForceExisting: *forceExisting,
		GitHub:        client,
		CommandRunner: env.CommandRunner,
		Now:           env.Now,
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "GitHub upload complete")
	return nil
}

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
	deleteRepo := fs.Bool("delete-repo", false, "delete the named GitHub repositories")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *owner == "" {
		return fmt.Errorf("delete github requires --owner")
	}
	if !*deleteRepo {
		return fmt.Errorf("delete github requires --delete-repo")
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

	client := env.GitHubClient
	if client == nil {
		client = github.NewClient(env.GitHubToken)
	}

	var failures []string
	for _, name := range names {
		repoName := rescue.SafeName(name)
		if err := client.DeleteRepository(ctx, *owner, repoName); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", repoName, err))
			continue
		}
		fmt.Fprintf(out, "Deleted %s/%s\n", *owner, repoName)
	}
	if len(failures) > 0 {
		return fmt.Errorf("deleted %d repos with %d failures: %s", len(names)-len(failures), len(failures), strings.Join(failures, "; "))
	}
	return nil
}

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

func usage(out io.Writer) error {
	fmt.Fprintln(out, "usage: forge-rescue scan --instance URL | forge-rescue rescue [--high-risk|--medium-risk] [owner/repo...] | forge-rescue upload github --owner OWNER | forge-rescue delete github --owner OWNER --delete-repo repo...")
	return nil
}
