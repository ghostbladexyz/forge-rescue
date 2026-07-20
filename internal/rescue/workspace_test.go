package rescue

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
)

// TestWorkspaceUsesDistinctIDBasedArtifactKeys verifies that flattened-name collisions cannot share new storage.
func TestWorkspaceUsesDistinctIDBasedArtifactKeys(t *testing.T) {
	root := t.TempDir()
	workspace, err := OpenWorkspace(root)
	if err != nil {
		t.Fatalf("OpenWorkspace returned error: %v", err)
	}
	scan := Scan{Instance: "https://git.example", Repos: []Repo{
		{ID: 41, FullName: "a/b-c"},
		{ID: 42, FullName: "a-b/c"},
	}}
	if err := workspace.SaveScan(scan); err != nil {
		t.Fatalf("SaveScan returned error: %v", err)
	}

	first, err := workspace.ArtifactFor(scan.Repos[0])
	if err != nil {
		t.Fatalf("first ArtifactFor returned error: %v", err)
	}
	second, err := workspace.ArtifactFor(scan.Repos[1])
	if err != nil {
		t.Fatalf("second ArtifactFor returned error: %v", err)
	}
	if first.Key != "repo-41" || second.Key != "repo-42" || first.MirrorPath == second.MirrorPath {
		t.Fatalf("artifact keys/paths = %q/%q and %q/%q, want distinct ID-based artifacts", first.Key, first.MirrorPath, second.Key, second.MirrorPath)
	}
}

// TestWorkspacePersistsCollisionSafeDestinationNames verifies readable names stay stable and colliding names receive source-ID suffixes.
func TestWorkspacePersistsCollisionSafeDestinationNames(t *testing.T) {
	root := t.TempDir()
	workspace, err := OpenWorkspace(root)
	if err != nil {
		t.Fatalf("OpenWorkspace returned error: %v", err)
	}
	scan := Scan{Instance: "https://git.example", Repos: []Repo{
		{ID: 41, FullName: "a/b-c"},
		{ID: 42, FullName: "a-b/c"},
		{ID: 43, FullName: "team/tool"},
	}}
	if err := workspace.SaveScan(scan); err != nil {
		t.Fatalf("SaveScan returned error: %v", err)
	}
	for _, repo := range scan.Repos {
		artifact, err := workspace.ArtifactFor(repo)
		if err != nil {
			t.Fatalf("ArtifactFor(%s) returned error: %v", repo.FullName, err)
		}
		if err := os.MkdirAll(artifact.MirrorPath, 0o700); err != nil {
			t.Fatalf("creating mirror for %s: %v", repo.FullName, err)
		}
	}

	first, err := workspace.UploadRepositories()
	if err != nil {
		t.Fatalf("UploadRepositories returned error: %v", err)
	}
	reopened, err := OpenWorkspace(root)
	if err != nil {
		t.Fatalf("reopening workspace: %v", err)
	}
	second, err := reopened.UploadRepositories()
	if err != nil {
		t.Fatalf("reopened UploadRepositories returned error: %v", err)
	}
	want := []string{"a-b-c-41", "a-b-c-42", "team-tool"}
	for i := range want {
		if first[i].DestinationName != want[i] || second[i].DestinationName != want[i] {
			t.Fatalf("destination names at %d = %q/%q, want stable %q", i, first[i].DestinationName, second[i].DestinationName, want[i])
		}
	}
}

// TestWorkspaceAllocatesValidBoundedDestinationNames verifies source-forge characters cannot produce invalid GitHub names.
func TestWorkspaceAllocatesValidBoundedDestinationNames(t *testing.T) {
	root := t.TempDir()
	workspace, err := OpenWorkspace(root)
	if err != nil {
		t.Fatalf("OpenWorkspace returned error: %v", err)
	}
	longName := strings.Repeat("x", 110)
	scan := Scan{Instance: "https://git.example", Repos: []Repo{
		{ID: 51, FullName: "space owner/repo+name"},
		{ID: 52, FullName: "team/" + longName},
	}}
	if err := workspace.SaveScan(scan); err != nil {
		t.Fatalf("SaveScan returned error: %v", err)
	}
	for _, repo := range scan.Repos {
		artifact, err := workspace.ArtifactFor(repo)
		if err != nil {
			t.Fatalf("ArtifactFor(%s) returned error: %v", repo.FullName, err)
		}
		if err := os.MkdirAll(artifact.MirrorPath, 0o700); err != nil {
			t.Fatalf("creating mirror for %s: %v", repo.FullName, err)
		}
	}

	repositories, err := workspace.UploadRepositories()
	if err != nil {
		t.Fatalf("UploadRepositories returned error: %v", err)
	}
	if repositories[0].DestinationName != "space-owner-repo-name" {
		t.Fatalf("sanitized destination = %q, want space-owner-repo-name", repositories[0].DestinationName)
	}
	if len(repositories[1].DestinationName) != 100 {
		t.Fatalf("long destination length = %d, want 100", len(repositories[1].DestinationName))
	}
}

// TestWorkspaceReadsUniqueLegacyMirrorWithoutMovingIt verifies dual-read compatibility leaves user data in place.
func TestWorkspaceReadsUniqueLegacyMirrorWithoutMovingIt(t *testing.T) {
	root := t.TempDir()
	scan := Scan{Instance: "https://git.example", Repos: []Repo{{FullName: "team/tool"}}}
	writeLegacyScan(t, root, scan)
	legacyMirror := filepath.Join(root, "repos", "team-tool.git")
	if err := os.MkdirAll(legacyMirror, 0o700); err != nil {
		t.Fatalf("creating legacy mirror: %v", err)
	}

	workspace, err := OpenWorkspace(root)
	if err != nil {
		t.Fatalf("OpenWorkspace returned error: %v", err)
	}
	rescued, err := workspace.RescuedRepositories()
	if err != nil {
		t.Fatalf("RescuedRepositories returned error: %v", err)
	}
	if len(rescued) != 1 || rescued[0].Artifact.MirrorPath != legacyMirror {
		t.Fatalf("rescued artifacts = %#v, want unique legacy mirror %q", rescued, legacyMirror)
	}
	if _, err := os.Stat(filepath.Join(root, "workspace.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only legacy lookup wrote workspace index: %v", err)
	}
}

// TestWorkspaceRecognizesCompleteLegacyMetadata verifies all four correctly shaped records preserve a completed archival capture.
func TestWorkspaceRecognizesCompleteLegacyMetadata(t *testing.T) {
	root := t.TempDir()
	repo := Repo{FullName: "team/tool"}
	writeLegacyScan(t, root, Scan{Instance: "https://git.example", Repos: []Repo{repo}})
	legacyMirror := filepath.Join(root, "repos", "team-tool.git")
	if err := os.MkdirAll(legacyMirror, 0o700); err != nil {
		t.Fatalf("creating legacy mirror: %v", err)
	}
	legacyMetadata := filepath.Join(root, "metadata", "team-tool")
	writeLegacyMetadata(t, legacyMetadata, map[string]string{
		"repo.json":     `{"full_name":"team/tool"}`,
		"issues.json":   `[{"id":1}]`,
		"releases.json": `[]`,
		"labels.json":   `[{"id":2}]`,
	})

	workspace, err := OpenWorkspace(root)
	if err != nil {
		t.Fatalf("OpenWorkspace returned error: %v", err)
	}
	artifact, err := workspace.ArtifactFor(repo)
	if err != nil {
		t.Fatalf("ArtifactFor returned error: %v", err)
	}
	if !artifact.MirrorComplete || !artifact.MetadataComplete {
		t.Fatalf("artifact completion = mirror:%t metadata:%t, want both complete", artifact.MirrorComplete, artifact.MetadataComplete)
	}
	if artifact.MirrorPath != legacyMirror || artifact.MetadataPath != legacyMetadata {
		t.Fatalf("artifact paths = %q/%q, want legacy paths %q/%q", artifact.MirrorPath, artifact.MetadataPath, legacyMirror, legacyMetadata)
	}
}

// TestWorkspaceKeepsPartialOrInvalidLegacyMetadataIncomplete verifies bad archival records never hide a resumable metadata phase.
func TestWorkspaceKeepsPartialOrInvalidLegacyMetadataIncomplete(t *testing.T) {
	tests := []struct {
		name    string
		records map[string]string
	}{
		{
			name: "missing labels",
			records: map[string]string{
				"repo.json":     `{"full_name":"team/tool"}`,
				"issues.json":   `[]`,
				"releases.json": `[]`,
			},
		},
		{
			name: "repository is not an object",
			records: map[string]string{
				"repo.json":     `[]`,
				"issues.json":   `[]`,
				"releases.json": `[]`,
				"labels.json":   `[]`,
			},
		},
		{
			name: "issues are not an array",
			records: map[string]string{
				"repo.json":     `{"full_name":"team/tool"}`,
				"issues.json":   `{}`,
				"releases.json": `[]`,
				"labels.json":   `[]`,
			},
		},
		{
			name: "release JSON is malformed",
			records: map[string]string{
				"repo.json":     `{"full_name":"team/tool"}`,
				"issues.json":   `[]`,
				"releases.json": `[`,
				"labels.json":   `[]`,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			repo := Repo{FullName: "team/tool"}
			writeLegacyScan(t, root, Scan{Instance: "https://git.example", Repos: []Repo{repo}})
			legacyMirror := filepath.Join(root, "repos", "team-tool.git")
			if err := os.MkdirAll(legacyMirror, 0o700); err != nil {
				t.Fatalf("creating legacy mirror: %v", err)
			}
			legacyMetadata := filepath.Join(root, "metadata", "team-tool")
			writeLegacyMetadata(t, legacyMetadata, test.records)

			workspace, err := OpenWorkspace(root)
			if err != nil {
				t.Fatalf("OpenWorkspace returned error: %v", err)
			}
			artifact, err := workspace.ArtifactFor(repo)
			if err != nil {
				t.Fatalf("ArtifactFor returned error: %v", err)
			}
			if !artifact.MirrorComplete || artifact.MetadataComplete {
				t.Fatalf("artifact completion = mirror:%t metadata:%t, want retained mirror and incomplete metadata", artifact.MirrorComplete, artifact.MetadataComplete)
			}
			if artifact.MirrorPath != legacyMirror || artifact.MetadataPath != legacyMetadata {
				t.Fatalf("artifact paths = %q/%q, want retained legacy paths %q/%q", artifact.MirrorPath, artifact.MetadataPath, legacyMirror, legacyMetadata)
			}
		})
	}
}

// TestMetadataRecoveryCannotAliasLegacyCanonicalPaths verifies transaction state never occupies another repository's flattened metadata name.
func TestMetadataRecoveryCannotAliasLegacyCanonicalPaths(t *testing.T) {
	root := t.TempDir()
	scan := Scan{
		Instance: "https://git.example",
		Repos: []Repo{
			{FullName: "team/a"},
			{FullName: "team/a.previous"},
		},
	}
	writeLegacyScan(t, root, scan)
	for _, repo := range scan.Repos {
		writeLegacyMetadata(t, filepath.Join(root, "metadata", SafeName(repo.FullName)), map[string]string{
			"repo.json":     `{"full_name":"` + repo.FullName + `"}`,
			"issues.json":   `[]`,
			"releases.json": `[]`,
			"labels.json":   `[]`,
		})
	}

	workspace, err := OpenWorkspace(root)
	if err != nil {
		t.Fatalf("OpenWorkspace returned error: %v", err)
	}
	if err := workspace.SaveScan(scan); err != nil {
		t.Fatalf("SaveScan returned error: %v", err)
	}
	first, err := workspace.ArtifactFor(scan.Repos[0])
	if err != nil {
		t.Fatalf("ArtifactFor first repository returned error: %v", err)
	}
	second, err := workspace.ArtifactFor(scan.Repos[1])
	if err != nil {
		t.Fatalf("ArtifactFor second repository returned error: %v", err)
	}
	backup := workspace.metadataBackupPath(first.Identity)
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		t.Fatalf("creating metadata backup directory: %v", err)
	}
	if err := os.Rename(first.MetadataPath, backup); err != nil {
		t.Fatalf("simulating interrupted metadata swap: %v", err)
	}

	if _, err := OpenWorkspace(root); err != nil {
		t.Fatalf("OpenWorkspace recovery returned error: %v", err)
	}
	for repoName, metadataPath := range map[string]string{
		scan.Repos[0].FullName: first.MetadataPath,
		scan.Repos[1].FullName: second.MetadataPath,
	} {
		record, err := os.ReadFile(filepath.Join(metadataPath, "repo.json"))
		if err != nil {
			t.Fatalf("reading recovered metadata for %s: %v", repoName, err)
		}
		if !strings.Contains(string(record), repoName) {
			t.Fatalf("metadata for %s = %s", repoName, record)
		}
	}
}

// TestWorkspaceRejectsAmbiguousLegacyCollision verifies that one flattened artifact is never guessed between two sources.
func TestWorkspaceRejectsAmbiguousLegacyCollision(t *testing.T) {
	root := t.TempDir()
	scan := Scan{Instance: "https://git.example", Repos: []Repo{{FullName: "a/b-c"}, {FullName: "a-b/c"}}}
	writeLegacyScan(t, root, scan)
	if err := os.MkdirAll(filepath.Join(root, "repos", "a-b-c.git"), 0o700); err != nil {
		t.Fatalf("creating ambiguous legacy mirror: %v", err)
	}

	workspace, err := OpenWorkspace(root)
	if err != nil {
		t.Fatalf("OpenWorkspace returned error: %v", err)
	}
	_, err = workspace.RescuedRepositories()
	if err == nil || !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "a/b-c") || !strings.Contains(err.Error(), "a-b/c") {
		t.Fatalf("RescuedRepositories error = %v, want both colliding repository names", err)
	}
}

// TestWorkspaceRejectsInstanceMismatch verifies that one workspace cannot silently change source forges.
func TestWorkspaceRejectsInstanceMismatch(t *testing.T) {
	workspace, err := OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("OpenWorkspace returned error: %v", err)
	}
	if err := workspace.SaveScan(Scan{Instance: "https://one.example", Repos: []Repo{{ID: 1, FullName: "a/repo"}}}); err != nil {
		t.Fatalf("first SaveScan returned error: %v", err)
	}
	err = workspace.SaveScan(Scan{Instance: "https://two.example", Repos: []Repo{{ID: 1, FullName: "a/repo"}}})
	if err == nil || !strings.Contains(err.Error(), "belongs to") {
		t.Fatalf("second SaveScan error = %v, want instance mismatch", err)
	}
}

// TestWorkspaceRejectsUnknownExplicitSelection verifies typos fail before any rescue side effects.
func TestWorkspaceRejectsUnknownExplicitSelection(t *testing.T) {
	workspace, err := OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("OpenWorkspace returned error: %v", err)
	}
	scan := Scan{Repos: []Repo{{FullName: "team/known"}}}
	_, err = workspace.Select(scan, Selection{Names: []string{"team/missing"}}, DefaultRiskConfig(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "team/missing") {
		t.Fatalf("Select error = %v, want unknown repository name", err)
	}
}

// TestRunRetainsMirrorAndResumesMetadata verifies a metadata failure records a partial artifact without recloning it.
func TestRunRetainsMirrorAndResumesMetadata(t *testing.T) {
	root := t.TempDir()
	scan := Scan{Instance: "https://git.example", Repos: []Repo{{ID: 91, FullName: "team/tool", CloneURL: "https://git.example/team/tool.git"}}}
	if err := WriteScan(filepath.Join(root, "scan.json"), scan); err != nil {
		t.Fatalf("WriteScan returned error: %v", err)
	}
	runner := &workspaceMirrors{}
	source := &flakyMetadataSource{failuresRemaining: 1}
	opts := Options{DataDir: root, GitMirrors: runner, MetadataSource: source, Now: fixedWorkspaceTime}
	previous := RepositoryMetadata{
		Repository: json.RawMessage(`{"full_name":"team/tool","capture":"previous"}`),
		Issues:     []json.RawMessage{},
		Releases:   []json.RawMessage{},
		Labels:     []json.RawMessage{},
	}
	workspace, err := OpenWorkspace(root)
	if err != nil {
		t.Fatalf("OpenWorkspace returned error: %v", err)
	}
	if err := workspace.SaveMetadata(scan.Repos[0], previous); err != nil {
		t.Fatalf("SaveMetadata returned error: %v", err)
	}
	artifact, err := workspace.ArtifactFor(scan.Repos[0])
	if err != nil {
		t.Fatalf("ArtifactFor returned error: %v", err)
	}

	if err := Run(context.Background(), opts); err == nil {
		t.Fatal("first Run returned nil, want metadata failure")
	}
	manifest, err := ReadManifest(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatalf("ReadManifest returned error: %v", err)
	}
	if len(manifest.Outcomes) != 1 || manifest.Outcomes[0].Status != OutcomePartial || !manifest.Outcomes[0].MirrorComplete || manifest.Outcomes[0].MetadataComplete {
		t.Fatalf("first outcome = %#v, want resumable partial mirror", manifest.Outcomes)
	}
	preserved, err := os.ReadFile(filepath.Join(artifact.MetadataPath, "repo.json"))
	if err != nil {
		t.Fatalf("reading preserved metadata: %v", err)
	}
	if !strings.Contains(string(preserved), `"capture": "previous"`) {
		t.Fatalf("preserved metadata = %s, want previous complete capture", preserved)
	}

	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}
	if runner.calls != 1 || source.calls != 2 {
		t.Fatalf("clone/capture calls = %d/%d, want 1/2", runner.calls, source.calls)
	}
}

// TestLoadScanRestoresMetadataInterruptedBetweenRenames verifies an already-open workspace repairs the canonical capture before a read.
func TestLoadScanRestoresMetadataInterruptedBetweenRenames(t *testing.T) {
	root := t.TempDir()
	repo := Repo{ID: 301, FullName: "team/interrupted"}
	workspace, artifact := prepareMetadataWorkspace(t, root, repo, metadataFixture("previous"))
	backup := workspace.metadataBackupPath(artifact.Identity)
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		t.Fatalf("creating metadata backup directory: %v", err)
	}
	if err := os.Rename(artifact.MetadataPath, backup); err != nil {
		t.Fatalf("simulating first metadata rename: %v", err)
	}

	if _, err := workspace.LoadScan(); err != nil {
		t.Fatalf("LoadScan returned error during recovery: %v", err)
	}
	assertMetadataCapture(t, artifact.MetadataPath, "previous")
	if pathExists(backup) {
		t.Fatalf("backup %q survived restoration", backup)
	}
}

// TestOpenWorkspaceKeepsInstalledMetadataAfterSecondRename verifies restart cleanup never replaces a completed new capture with its backup.
func TestOpenWorkspaceKeepsInstalledMetadataAfterSecondRename(t *testing.T) {
	root := t.TempDir()
	repo := Repo{ID: 302, FullName: "team/installed"}
	workspace, artifact := prepareMetadataWorkspace(t, root, repo, metadataFixture("previous"))
	backup := workspace.metadataBackupPath(artifact.Identity)
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		t.Fatalf("creating metadata backup directory: %v", err)
	}
	if err := os.Rename(artifact.MetadataPath, backup); err != nil {
		t.Fatalf("simulating first metadata rename: %v", err)
	}
	writeMetadataDirectory(t, artifact.MetadataPath, metadataFixture("installed"))

	if _, err := OpenWorkspace(root); err != nil {
		t.Fatalf("OpenWorkspace returned error: %v", err)
	}
	assertMetadataCapture(t, artifact.MetadataPath, "installed")
	if pathExists(backup) {
		t.Fatalf("obsolete backup %q survived completed-swap recovery", backup)
	}
}

// TestSaveMetadataIgnoresCompletedSwapCleanupFailure verifies a durable new capture succeeds even when its obsolete backup cannot yet be removed.
func TestSaveMetadataIgnoresCompletedSwapCleanupFailure(t *testing.T) {
	root := t.TempDir()
	repo := Repo{ID: 303, FullName: "team/cleanup"}
	workspace, artifact := prepareMetadataWorkspace(t, root, repo, metadataFixture("previous"))
	workspace.removeMetadataBackup = func(path string) error {
		return errors.New("cleanup denied")
	}
	if err := workspace.SaveMetadata(repo, metadataFixture("installed")); err != nil {
		t.Fatalf("SaveMetadata returned cleanup error after installation: %v", err)
	}
	assertMetadataCapture(t, artifact.MetadataPath, "installed")
	backup := workspace.metadataBackupPath(artifact.Identity)
	if !pathExists(backup) {
		t.Fatalf("backup %q was unexpectedly removed by failing cleanup adapter", backup)
	}

	if _, err := OpenWorkspace(root); err != nil {
		t.Fatalf("OpenWorkspace returned error while cleaning obsolete backup: %v", err)
	}
	assertMetadataCapture(t, artifact.MetadataPath, "installed")
	if pathExists(backup) {
		t.Fatalf("backup %q survived later recovery", backup)
	}
}

type workspaceMirrors struct {
	calls int
}

// Clone creates the requested artifact so a later rescue observes the retained mirror without exposing Git commands.
func (r *workspaceMirrors) Clone(ctx context.Context, source gitmirror.Remote, destination string) error {
	r.calls++
	return os.MkdirAll(destination, 0o700)
}

type flakyMetadataSource struct {
	calls             int
	failuresRemaining int
}

// prepareMetadataWorkspace creates an indexed workspace with one complete repository metadata capture.
func prepareMetadataWorkspace(t *testing.T, root string, repo Repo, metadata RepositoryMetadata) (*Workspace, Artifact) {
	t.Helper()
	workspace, err := OpenWorkspace(root)
	if err != nil {
		t.Fatalf("OpenWorkspace returned error: %v", err)
	}
	if err := workspace.SaveScan(Scan{Instance: "https://git.example", Repos: []Repo{repo}}); err != nil {
		t.Fatalf("SaveScan returned error: %v", err)
	}
	if err := workspace.SaveMetadata(repo, metadata); err != nil {
		t.Fatalf("SaveMetadata returned error: %v", err)
	}
	artifact, err := workspace.ArtifactFor(repo)
	if err != nil {
		t.Fatalf("ArtifactFor returned error: %v", err)
	}
	return workspace, artifact
}

// metadataFixture returns a complete capture whose repository record identifies the expected transaction generation.
func metadataFixture(generation string) RepositoryMetadata {
	return RepositoryMetadata{
		Repository: json.RawMessage(`{"generation":"` + generation + `"}`),
		Issues:     []json.RawMessage{},
		Releases:   []json.RawMessage{},
		Labels:     []json.RawMessage{},
	}
}

// writeMetadataDirectory materializes a complete staged capture to simulate the second durable rename.
func writeMetadataDirectory(t *testing.T, path string, metadata RepositoryMetadata) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("creating metadata directory: %v", err)
	}
	records := []struct {
		name  string
		value any
	}{
		{name: "repo.json", value: metadata.Repository},
		{name: "issues.json", value: metadata.Issues},
		{name: "releases.json", value: metadata.Releases},
		{name: "labels.json", value: metadata.Labels},
	}
	for _, record := range records {
		if err := writeJSONAtomic(filepath.Join(path, record.name), record.value); err != nil {
			t.Fatalf("writing %s: %v", record.name, err)
		}
	}
}

// assertMetadataCapture verifies the canonical repository record belongs to the expected completed generation.
func assertMetadataCapture(t *testing.T, path, generation string) {
	t.Helper()
	record, err := os.ReadFile(filepath.Join(path, "repo.json"))
	if err != nil {
		t.Fatalf("reading repository metadata: %v", err)
	}
	if !strings.Contains(string(record), `"generation": "`+generation+`"`) {
		t.Fatalf("repository metadata = %s, want generation %q", record, generation)
	}
}

// CaptureMetadata fails a configured number of attempts to exercise resumable partial outcomes.
func (s *flakyMetadataSource) CaptureMetadata(ctx context.Context, repo Repo) (RepositoryMetadata, error) {
	s.calls++
	if s.failuresRemaining > 0 {
		s.failuresRemaining--
		return RepositoryMetadata{}, errors.New("metadata unavailable")
	}
	return RepositoryMetadata{
		Repository: json.RawMessage(`{"full_name":"` + repo.FullName + `"}`),
		Issues:     []json.RawMessage{},
		Releases:   []json.RawMessage{},
		Labels:     []json.RawMessage{},
	}, nil
}

// writeLegacyScan creates an index-free scan fixture because WriteScan intentionally creates new versioned workspaces.
func writeLegacyScan(t *testing.T, root string, scan Scan) {
	t.Helper()
	data, err := json.MarshalIndent(scan, "", "  ")
	if err != nil {
		t.Fatalf("encoding legacy scan: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "scan.json"), append(data, '\n'), 0o600); err != nil {
		t.Fatalf("writing legacy scan: %v", err)
	}
}

// writeLegacyMetadata creates exactly the supplied archival records so tests can distinguish complete and partial captures.
func writeLegacyMetadata(t *testing.T, path string, records map[string]string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("creating legacy metadata directory: %v", err)
	}
	for name, contents := range records {
		if err := os.WriteFile(filepath.Join(path, name), []byte(contents), 0o600); err != nil {
			t.Fatalf("writing legacy metadata %s: %v", name, err)
		}
	}
}

// fixedWorkspaceTime makes manifest timestamps stable across both rescue attempts.
func fixedWorkspaceTime() time.Time {
	return time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
}
