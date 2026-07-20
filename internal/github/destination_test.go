package github

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ghostbladexyz/forge-rescue/internal/gitmirror"
	"github.com/ghostbladexyz/forge-rescue/internal/rescue"
)

// TestUploadResolvesUserAndOrganizationCreation verifies owner resolution chooses the only valid GitHub creation endpoint.
func TestUploadResolvesUserAndOrganizationCreation(t *testing.T) {
	tests := []struct {
		name       string
		owner      string
		wantCreate string
	}{
		{name: "authenticated user case insensitive", owner: "Alice", wantCreate: "user/team-project"},
		{name: "organization", owner: "rescue-org", wantCreate: "org:rescue-org/team-project"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workspace := uploadWorkspace(t, []rescue.Repo{{ID: 7, FullName: "team/project"}})
			repositories := &recordingRepositoryPort{
				authenticatedUser: "alice",
				exists:            map[string]bool{},
				memberships:       map[string]bool{"rescue-org": true},
			}
			mirrors := &recordingPublisher{}
			destination := newDestination("token", repositories, mirrors, fixedTime)

			report, err := destination.Upload(context.Background(), UploadRequest{Workspace: workspace, Owner: test.owner})
			if err != nil {
				t.Fatalf("Upload returned error: %v", err)
			}
			if len(repositories.created) != 1 || repositories.created[0] != test.wantCreate {
				t.Fatalf("created repositories = %#v, want %q", repositories.created, test.wantCreate)
			}
			if report.Success != 1 || report.Outcomes[0].Status != UploadCreated || !report.Outcomes[0].Created || mirrors.pushes != 1 {
				t.Fatalf("report/push = %#v/%d, want one created upload", report, mirrors.pushes)
			}
		})
	}
}

// TestUploadSkipsNonemptyByDefaultAndReplacesOnlyExplicitly verifies destructive mirror behavior stays behind a named policy.
func TestUploadSkipsNonemptyByDefaultAndReplacesOnlyExplicitly(t *testing.T) {
	workspace := uploadWorkspace(t, []rescue.Repo{{ID: 8, FullName: "team/project"}})
	repositories := &recordingRepositoryPort{
		authenticatedUser: "alice",
		exists:            map[string]bool{"alice/team-project": true},
		refs:              map[string]bool{"alice/team-project": true},
	}
	defaultMirrors := &recordingPublisher{}
	defaultDestination := newDestination("token", repositories, defaultMirrors, fixedTime)
	report, err := defaultDestination.Upload(context.Background(), UploadRequest{Workspace: workspace, Owner: "alice"})
	if err != nil {
		t.Fatalf("default Upload returned error: %v", err)
	}
	if report.Skipped != 1 || report.Failed != 0 || report.Outcomes[0].Status != UploadSkippedNonempty || defaultMirrors.pushes != 0 {
		t.Fatalf("default report/push = %#v/%d, want safe skip", report, defaultMirrors.pushes)
	}

	replaceMirrors := &recordingPublisher{}
	replaceDestination := newDestination("token", repositories, replaceMirrors, fixedTime)
	report, err = replaceDestination.Upload(context.Background(), UploadRequest{Workspace: workspace, Owner: "alice", Existing: ReplaceExistingRefs})
	if err != nil {
		t.Fatalf("replacement Upload returned error: %v", err)
	}
	if report.Success != 1 || report.Outcomes[0].Status != UploadReplacedRefs || replaceMirrors.pushes != 1 {
		t.Fatalf("replacement report/push = %#v/%d, want explicit replacement", report, replaceMirrors.pushes)
	}
}

// TestUploadRecordsFailuresAndPersistsStructuredOutcomes verifies one failed push does not erase a successful batch report.
func TestUploadRecordsFailuresAndPersistsStructuredOutcomes(t *testing.T) {
	workspace := uploadWorkspace(t, []rescue.Repo{{ID: 9, FullName: "team/project"}})
	destination := newDestination("token", &recordingRepositoryPort{authenticatedUser: "alice"}, &recordingPublisher{err: errors.New("push rejected")}, fixedTime)
	report, err := destination.Upload(context.Background(), UploadRequest{Workspace: workspace, Owner: "alice"})
	if err == nil {
		t.Fatal("Upload returned nil, want batch failure")
	}
	if report.Failed != 1 || report.Outcomes[0].Status != UploadFailed || !strings.Contains(report.Outcomes[0].Error, "push rejected") {
		t.Fatalf("report = %#v, want failed push outcome", report)
	}

	var persisted UploadReport
	data := readWorkspaceReport(t, workspace, "upload-github.json")
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("decoding persisted report: %v", err)
	}
	if persisted.Failed != 1 || len(persisted.Outcomes) != 1 {
		t.Fatalf("persisted report = %#v, want one failure", persisted)
	}
}

// TestUploadRejectsInvalidAndOtherUserOwners verifies an owner must be the authenticated user or an active organization.
func TestUploadRejectsInvalidAndOtherUserOwners(t *testing.T) {
	tests := []struct {
		name  string
		owner string
	}{
		{name: "invalid owner", owner: "other/user"},
		{name: "invalid owner characters", owner: "other_user"},
		{name: "different user", owner: "bob"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workspace := uploadWorkspace(t, []rescue.Repo{{ID: 10, FullName: "team/project"}})
			repositories := &recordingRepositoryPort{authenticatedUser: "alice"}
			mirrors := &recordingPublisher{}
			destination := newDestination("token", repositories, mirrors, fixedTime)

			_, err := destination.Upload(context.Background(), UploadRequest{Workspace: workspace, Owner: test.owner})
			if err == nil {
				t.Fatalf("Upload(%q) returned nil, want owner preflight error", test.owner)
			}
			if repositories.inspections != 0 || len(repositories.created) != 0 || mirrors.pushes != 0 {
				t.Fatalf("owner preflight mutated inspection/create/push = %d/%#v/%d", repositories.inspections, repositories.created, mirrors.pushes)
			}
			if strings.HasPrefix(test.name, "invalid owner") && repositories.authenticatedCalls != 0 {
				t.Fatalf("invalid owner made %d authenticated-user requests", repositories.authenticatedCalls)
			}
		})
	}
}

// TestUploadRejectsUnauthorizedOrganization verifies membership errors cannot fall through to organization creation.
func TestUploadRejectsUnauthorizedOrganization(t *testing.T) {
	workspace := uploadWorkspace(t, []rescue.Repo{{ID: 11, FullName: "team/project"}})
	repositories := &recordingRepositoryPort{
		authenticatedUser: "alice",
		membershipErrors:  map[string]error{"blocked-org": errors.New("forbidden")},
	}
	mirrors := &recordingPublisher{}
	destination := newDestination("token", repositories, mirrors, fixedTime)

	_, err := destination.Upload(context.Background(), UploadRequest{Workspace: workspace, Owner: "blocked-org"})
	if err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("Upload error = %v, want organization authorization failure", err)
	}
	if len(repositories.created) != 0 || repositories.inspections != 0 || mirrors.pushes != 0 {
		t.Fatalf("unauthorized organization mutated create/inspection/push = %#v/%d/%d", repositories.created, repositories.inspections, mirrors.pushes)
	}
}

// TestOwnerPreflightPrecedesWorkspaceMutation verifies rejected owners cannot persist destination allocation locally.
func TestOwnerPreflightPrecedesWorkspaceMutation(t *testing.T) {
	workspace := uploadWorkspace(t, []rescue.Repo{{ID: 12, FullName: "team/project"}})
	rescued, err := workspace.RescuedRepositories()
	if err != nil || len(rescued) != 1 {
		t.Fatalf("RescuedRepositories = %#v, %v", rescued, err)
	}
	indexPath := filepath.Join(filepath.Dir(filepath.Dir(rescued[0].Artifact.MirrorPath)), "workspace.json")
	before, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("reading workspace before preflight: %v", err)
	}
	repositories := &recordingRepositoryPort{authenticatedUser: "alice"}
	destination := newDestination("token", repositories, &recordingPublisher{}, fixedTime)

	_, err = destination.Upload(context.Background(), UploadRequest{Workspace: workspace, Owner: "bob"})
	if err == nil {
		t.Fatal("Upload returned nil, want owner preflight error")
	}
	after, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("reading workspace after preflight: %v", err)
	}
	if string(after) != string(before) || strings.Contains(string(after), "destination_name") {
		t.Fatalf("workspace changed before owner validation:\n%s", after)
	}
}

// TestDeletePreflightsExactNamesBeforeMutation verifies source-style names and case-insensitive duplicates fail closed.
func TestDeletePreflightsExactNamesBeforeMutation(t *testing.T) {
	tests := [][]string{{"team/project"}, {"Project", "project"}}
	for _, names := range tests {
		repositories := &recordingRepositoryPort{authenticatedUser: "alice"}
		destination := newDestination("token", repositories, &recordingPublisher{}, fixedTime)
		_, err := destination.Delete(context.Background(), DeleteRequest{Owner: "alice", Repositories: names})
		if err == nil {
			t.Fatalf("Delete(%#v) returned nil, want preflight error", names)
		}
		if len(repositories.deleted) != 0 {
			t.Fatalf("Delete(%#v) mutated %#v before preflight completed", names, repositories.deleted)
		}
	}
}

// TestDeleteOwnerPreflightPrecedesMutation verifies an unaffiliated owner is rejected before the first irreversible delete.
func TestDeleteOwnerPreflightPrecedesMutation(t *testing.T) {
	repositories := &recordingRepositoryPort{authenticatedUser: "alice"}
	destination := newDestination("token", repositories, &recordingPublisher{}, fixedTime)

	_, err := destination.Delete(context.Background(), DeleteRequest{Owner: "bob", Repositories: []string{"project"}})
	if err == nil {
		t.Fatal("Delete returned nil, want owner preflight error")
	}
	if len(repositories.deleted) != 0 {
		t.Fatalf("Delete mutated repositories before owner preflight: %#v", repositories.deleted)
	}
}

// TestDeleteReportsDeletedSkippedAndFailed verifies exact HTTP outcomes remain distinct in the workflow report.
func TestDeleteReportsDeletedSkippedAndFailed(t *testing.T) {
	repositories := &recordingRepositoryPort{
		authenticatedUser: "alice",
		deleteResults:     map[string]bool{"alice/removed": true},
		deleteErrors:      map[string]error{"alice/blocked": errors.New("forbidden")},
	}
	destination := newDestination("token", repositories, &recordingPublisher{}, fixedTime)
	report, err := destination.Delete(context.Background(), DeleteRequest{
		Owner:        "alice",
		Repositories: []string{"removed", "missing", "blocked"},
	})
	if err == nil {
		t.Fatal("Delete returned nil, want partial failure")
	}
	if report.Deleted != 1 || report.Skipped != 1 || report.Failed != 1 {
		t.Fatalf("delete report = %#v, want deleted/skipped/failed 1/1/1", report)
	}
	if got := []string{report.Outcomes[0].Status, report.Outcomes[1].Status, report.Outcomes[2].Status}; got[0] != DeleteRemoved || got[1] != DeleteSkippedNotFound || got[2] != DeleteFailed {
		t.Fatalf("delete statuses = %#v", got)
	}
}

type recordingRepositoryPort struct {
	authenticatedUser  string
	authenticatedCalls int
	memberships        map[string]bool
	membershipErrors   map[string]error
	membershipCalls    []string
	inspections        int
	exists             map[string]bool
	refs               map[string]bool
	created            []string
	deleted            []string
	deleteResults      map[string]bool
	deleteErrors       map[string]error
}

// AuthenticatedUser returns the configured login used to choose user or organization creation.
func (r *recordingRepositoryPort) AuthenticatedUser(ctx context.Context) (string, error) {
	r.authenticatedCalls++
	return r.authenticatedUser, nil
}

// ActiveOrganizationMembership returns the configured active state or authorization failure for one organization.
func (r *recordingRepositoryPort) ActiveOrganizationMembership(ctx context.Context, owner string) (bool, error) {
	r.membershipCalls = append(r.membershipCalls, owner)
	return r.memberships[owner], r.membershipErrors[owner]
}

// RepositoryExists returns the configured destination state without remote I/O.
func (r *recordingRepositoryPort) RepositoryExists(ctx context.Context, owner, name string) (bool, error) {
	r.inspections++
	return r.exists[owner+"/"+name], nil
}

// CreateUserRepository records use of the authenticated-user endpoint and makes later inspection observe the repository.
func (r *recordingRepositoryPort) CreateUserRepository(ctx context.Context, name string) error {
	r.created = append(r.created, "user/"+name)
	return nil
}

// CreateOrganizationRepository records the organization whose endpoint receives the repository.
func (r *recordingRepositoryPort) CreateOrganizationRepository(ctx context.Context, owner, name string) error {
	r.created = append(r.created, "org:"+owner+"/"+name)
	return nil
}

// HasRefs returns the configured ref state used by safe existing-repository policy.
func (r *recordingRepositoryPort) HasRefs(ctx context.Context, owner, name string) (bool, error) {
	return r.refs[owner+"/"+name], nil
}

// DeleteRepository records exact destination names and returns their configured outcome.
func (r *recordingRepositoryPort) DeleteRepository(ctx context.Context, owner, name string) (bool, error) {
	key := owner + "/" + name
	r.deleted = append(r.deleted, key)
	return r.deleteResults[key], r.deleteErrors[key]
}

type recordingPublisher struct {
	pushes int
	err    error
}

// Push records publication while Git argument and credential safety remain covered by gitmirror tests.
func (p *recordingPublisher) Push(ctx context.Context, mirrorPath string, destination gitmirror.Remote) error {
	p.pushes++
	return p.err
}

// uploadWorkspace creates complete mirror artifacts through the workspace interface used by Upload.
func uploadWorkspace(t *testing.T, repositories []rescue.Repo) *rescue.Workspace {
	t.Helper()
	root := t.TempDir()
	workspace, err := rescue.OpenWorkspace(root)
	if err != nil {
		t.Fatalf("OpenWorkspace returned error: %v", err)
	}
	if err := workspace.SaveScan(rescue.Scan{Instance: "https://git.example", Repos: repositories}); err != nil {
		t.Fatalf("SaveScan returned error: %v", err)
	}
	for _, repository := range repositories {
		artifact, err := workspace.ArtifactFor(repository)
		if err != nil {
			t.Fatalf("ArtifactFor returned error: %v", err)
		}
		if err := os.MkdirAll(artifact.MirrorPath, 0o700); err != nil {
			t.Fatalf("creating mirror: %v", err)
		}
	}
	return workspace
}

// readWorkspaceReport locates the report through the mirror's workspace root for persistence assertions.
func readWorkspaceReport(t *testing.T, workspace *rescue.Workspace, filename string) []byte {
	t.Helper()
	rescued, err := workspace.RescuedRepositories()
	if err != nil || len(rescued) == 0 {
		t.Fatalf("RescuedRepositories = %#v, %v", rescued, err)
	}
	root := filepath.Dir(filepath.Dir(rescued[0].Artifact.MirrorPath))
	data, err := os.ReadFile(filepath.Join(root, filename))
	if err != nil {
		t.Fatalf("reading report: %v", err)
	}
	return data
}

// fixedTime makes workflow report timestamps deterministic.
func fixedTime() time.Time {
	return time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
}
