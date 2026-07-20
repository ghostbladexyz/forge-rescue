// Package github owns repository publication and deletion policy for a GitHub destination.
package github

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/ghostbladexyz/forge-rescue/internal/gitmirror"
	"github.com/ghostbladexyz/forge-rescue/internal/rescue"
)

// ExistingPolicy controls whether upload may replace refs in a nonempty destination repository.
type ExistingPolicy uint8

const (
	CreateOrFillEmpty ExistingPolicy = iota
	ReplaceExistingRefs
)

const (
	UploadCreated         = "created_and_uploaded"
	UploadFilledEmpty     = "uploaded_to_empty"
	UploadReplacedRefs    = "replaced_existing_refs"
	UploadSkippedNonempty = "skipped_nonempty"
	UploadFailed          = "failed"
	DeleteRemoved         = "deleted"
	DeleteSkippedNotFound = "skipped_not_found_or_inaccessible"
	DeleteFailed          = "failed"
)

// UploadRequest describes one sequential publication of all complete artifacts in a rescue workspace.
type UploadRequest struct {
	Workspace *rescue.Workspace
	Owner     string
	Existing  ExistingPolicy
}

// UploadReport records every destination decision so safe skips remain distinct from failures.
type UploadReport struct {
	Provider   string          `json:"provider"`
	Owner      string          `json:"owner"`
	UploadedAt time.Time       `json:"uploaded_at"`
	ReposTotal int             `json:"repos_total"`
	Success    int             `json:"success"`
	Failed     int             `json:"failed"`
	Skipped    int             `json:"skipped"`
	Outcomes   []UploadOutcome `json:"outcomes,omitempty"`
}

// UploadOutcome relates one stable source identity to its allocated GitHub destination and observable result.
type UploadOutcome struct {
	SourceRepository      string `json:"source_repository"`
	SourceIdentity        string `json:"source_identity"`
	DestinationRepository string `json:"destination_repository"`
	Status                string `json:"status"`
	Created               bool   `json:"created,omitempty"`
	Error                 string `json:"error,omitempty"`
}

// DeleteRequest names exact GitHub repositories after CLI confirmation has been validated.
type DeleteRequest struct {
	Owner        string
	Repositories []string
}

// DeleteReport records every deletion attempt, including idempotent not-found outcomes.
type DeleteReport struct {
	Provider string          `json:"provider"`
	Owner    string          `json:"owner"`
	Deleted  int             `json:"deleted"`
	Failed   int             `json:"failed"`
	Skipped  int             `json:"skipped"`
	Outcomes []DeleteOutcome `json:"outcomes,omitempty"`
}

// DeleteOutcome records the exact destination name supplied by the caller without source-name conversion.
type DeleteOutcome struct {
	Repository string `json:"repository"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
}

type repositoryPort interface {
	AuthenticatedUser(ctx context.Context) (string, error)
	ActiveOrganizationMembership(ctx context.Context, owner string) (bool, error)
	RepositoryExists(ctx context.Context, owner, name string) (bool, error)
	CreateUserRepository(ctx context.Context, name string) error
	CreateOrganizationRepository(ctx context.Context, owner, name string) error
	HasRefs(ctx context.Context, owner, name string) (bool, error)
	DeleteRepository(ctx context.Context, owner, name string) (bool, error)
}

type mirrorPublisher interface {
	Push(ctx context.Context, mirrorPath string, destination gitmirror.Remote) error
}

// Destination hides GitHub owner resolution, repository policy, HTTP transport, Git authentication, and reporting.
type Destination struct {
	token        string
	repositories repositoryPort
	mirrors      mirrorPublisher
	now          func() time.Time
}

// New constructs the production GitHub destination with operation-scoped Git credentials.
func New(token string) *Destination {
	return &Destination{
		token:        token,
		repositories: newHTTPAdapter("https://api.github.com", token, nil),
		mirrors:      gitmirror.New(),
		now:          time.Now,
	}
}

// newDestination constructs a testable destination while keeping both transport seams inside this package.
func newDestination(token string, repositories repositoryPort, mirrors mirrorPublisher, now func() time.Time) *Destination {
	return &Destination{token: token, repositories: repositories, mirrors: mirrors, now: now}
}

// Upload publishes complete rescue artifacts sequentially and persists the structured report before returning batch failures.
func (d *Destination) Upload(ctx context.Context, request UploadRequest) (UploadReport, error) {
	report := UploadReport{Provider: "github", Owner: request.Owner}
	if err := d.validateUpload(request); err != nil {
		return report, err
	}
	userOwner, err := d.resolveOwner(ctx, request.Owner)
	if err != nil {
		return report, err
	}
	now := d.now
	if now == nil {
		now = time.Now
	}
	report.UploadedAt = now().UTC()
	repositories, err := request.Workspace.UploadRepositories()
	if err != nil {
		return report, fmt.Errorf("prepare upload repositories: %w", err)
	}
	report.ReposTotal = len(repositories)

	for _, repository := range repositories {
		outcome := d.uploadOne(ctx, request.Owner, userOwner, request.Existing, repository)
		report.Outcomes = append(report.Outcomes, outcome)
		switch outcome.Status {
		case UploadCreated, UploadFilledEmpty, UploadReplacedRefs:
			report.Success++
		case UploadSkippedNonempty:
			report.Skipped++
		default:
			report.Failed++
		}
		if ctx.Err() != nil {
			break // Cancellation stops new remote mutations while preserving outcomes already completed by this run.
		}
	}

	if err := request.Workspace.WriteReport("upload-github.json", report); err != nil {
		return report, fmt.Errorf("write GitHub upload report: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if report.Failed > 0 {
		return report, fmt.Errorf("uploaded %d repositories with %d failures and %d skips", report.Success, report.Failed, report.Skipped)
	}
	return report, nil
}

// Delete validates the entire exact-name set before deleting sequentially so malformed later input cannot cause a partial batch.
func (d *Destination) Delete(ctx context.Context, request DeleteRequest) (DeleteReport, error) {
	report := DeleteReport{Provider: "github", Owner: request.Owner}
	if err := d.validateDelete(request); err != nil {
		return report, err
	}
	if _, err := d.resolveOwner(ctx, request.Owner); err != nil {
		return report, err
	}
	for _, name := range request.Repositories {
		outcome := DeleteOutcome{Repository: name}
		deleted, err := d.repositories.DeleteRepository(ctx, request.Owner, name)
		switch {
		case err != nil:
			outcome.Status = DeleteFailed
			outcome.Error = err.Error()
			report.Failed++
		case deleted:
			outcome.Status = DeleteRemoved
			report.Deleted++
		default:
			// GitHub intentionally uses 404 for both absence and inaccessible private repositories, so the report avoids claiming which occurred.
			outcome.Status = DeleteSkippedNotFound
			report.Skipped++
		}
		report.Outcomes = append(report.Outcomes, outcome)
		if ctx.Err() != nil {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if report.Failed > 0 {
		return report, fmt.Errorf("deleted %d repositories with %d failures and %d skips", report.Deleted, report.Failed, report.Skipped)
	}
	return report, nil
}

// resolveOwner accepts only the authenticated user or an active organization membership before any local or remote mutation begins.
func (d *Destination) resolveOwner(ctx context.Context, owner string) (bool, error) {
	authenticatedUser, err := d.repositories.AuthenticatedUser(ctx)
	if err != nil {
		return false, fmt.Errorf("resolve authenticated GitHub user: %w", err)
	}
	if strings.EqualFold(authenticatedUser, owner) {
		return true, nil
	}
	active, err := d.repositories.ActiveOrganizationMembership(ctx, owner)
	if err != nil {
		return false, fmt.Errorf("validate GitHub organization %q: %w", owner, err)
	}
	if !active {
		return false, fmt.Errorf("GitHub owner %q is neither the authenticated user nor an active accessible organization", owner)
	}
	return false, nil
}

// uploadOne applies create/fill/replace policy before publishing one workspace-owned mirror.
func (d *Destination) uploadOne(ctx context.Context, owner string, userOwner bool, policy ExistingPolicy, repository rescue.RescuedRepository) UploadOutcome {
	outcome := UploadOutcome{
		SourceRepository:      repository.Repository.FullName,
		SourceIdentity:        repository.Artifact.Identity,
		DestinationRepository: repository.DestinationName,
	}
	exists, err := d.repositories.RepositoryExists(ctx, owner, repository.DestinationName)
	if err != nil {
		return failedUpload(outcome, err)
	}
	if !exists {
		if userOwner {
			err = d.repositories.CreateUserRepository(ctx, repository.DestinationName)
		} else {
			err = d.repositories.CreateOrganizationRepository(ctx, owner, repository.DestinationName)
		}
		if err != nil {
			return failedUpload(outcome, err)
		}
		outcome.Created = true
	} else if policy == CreateOrFillEmpty {
		hasRefs, refsErr := d.repositories.HasRefs(ctx, owner, repository.DestinationName)
		if refsErr != nil {
			return failedUpload(outcome, refsErr)
		}
		if hasRefs {
			outcome.Status = UploadSkippedNonempty
			return outcome
		}
	}

	remoteURL := "https://github.com/" + url.PathEscape(owner) + "/" + url.PathEscape(repository.DestinationName) + ".git"
	remote, err := gitmirror.NewAuthenticatedRemote(remoteURL, "x-access-token", d.token)
	if err != nil {
		return failedUpload(outcome, err)
	}
	if err := d.mirrors.Push(ctx, repository.Artifact.MirrorPath, remote); err != nil {
		return failedUpload(outcome, err)
	}
	switch {
	case outcome.Created:
		outcome.Status = UploadCreated
	case policy == ReplaceExistingRefs:
		outcome.Status = UploadReplacedRefs
	default:
		outcome.Status = UploadFilledEmpty
	}
	return outcome
}

// validateUpload rejects incomplete configuration before destination names are persisted or remote repositories are inspected.
func (d *Destination) validateUpload(request UploadRequest) error {
	if request.Workspace == nil {
		return fmt.Errorf("upload requires a rescue workspace")
	}
	if err := validateOwner(request.Owner); err != nil {
		return err
	}
	if d.token == "" {
		return fmt.Errorf("set GITHUB_TOKEN or provide a GitHub token in the environment")
	}
	if d.repositories == nil || d.mirrors == nil {
		return fmt.Errorf("GitHub destination is incomplete")
	}
	if request.Existing != CreateOrFillEmpty && request.Existing != ReplaceExistingRefs {
		return fmt.Errorf("unknown existing repository policy %d", request.Existing)
	}
	return nil
}

// validateDelete rejects ambiguous or duplicate exact names before the first irreversible request.
func (d *Destination) validateDelete(request DeleteRequest) error {
	if err := validateOwner(request.Owner); err != nil {
		return err
	}
	if d.token == "" {
		return fmt.Errorf("set GITHUB_TOKEN or provide a GitHub token in the environment")
	}
	if d.repositories == nil {
		return fmt.Errorf("GitHub destination is incomplete")
	}
	if len(request.Repositories) == 0 {
		return fmt.Errorf("delete github requires at least one exact repository name")
	}
	seen := make(map[string]struct{}, len(request.Repositories))
	for _, name := range request.Repositories {
		if err := validateRepositoryName(name); err != nil {
			return err
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate GitHub repository name %q", name)
		}
		seen[key] = struct{}{}
	}
	return nil
}

// validateOwner enforces GitHub login characters locally so malformed owners never reach identity or membership lookups.
func validateOwner(owner string) error {
	if owner == "" || len(owner) > 39 || owner[0] == '-' || owner[len(owner)-1] == '-' {
		return fmt.Errorf("GitHub owner must be one exact account name")
	}
	previousHyphen := false
	for _, character := range owner {
		alphanumeric := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9'
		if !alphanumeric && character != '-' {
			return fmt.Errorf("GitHub owner must be one exact account name")
		}
		if character == '-' && previousHyphen {
			return fmt.Errorf("GitHub owner must be one exact account name")
		}
		previousHyphen = character == '-'
	}
	return nil
}

// validateRepositoryName accepts GitHub's documented repository characters while rejecting names the platform would normalize.
func validateRepositoryName(name string) error {
	if name == "" || len(name) > 100 || name == "." || name == ".." || strings.TrimSpace(name) != name || strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("invalid exact GitHub repository name %q", name)
	}
	for _, character := range name {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune("._-", character) {
			continue
		}
		return fmt.Errorf("invalid exact GitHub repository name %q", name)
	}
	return nil
}

// failedUpload converts one operational error into a report-safe per-repository outcome.
func failedUpload(outcome UploadOutcome, err error) UploadOutcome {
	outcome.Status = UploadFailed
	outcome.Error = err.Error()
	return outcome
}
