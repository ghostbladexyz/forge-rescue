package rescue

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const workspaceVersion = 1

type workspaceIndex struct {
	Version      int                   `json:"version"`
	Instance     string                `json:"instance"`
	Repositories []workspaceRepository `json:"repositories"`
}

type workspaceRepository struct {
	ID               int64  `json:"id,omitempty"`
	FullName         string `json:"full_name"`
	Identity         string `json:"identity"`
	ArtifactKey      string `json:"artifact_key"`
	DestinationName  string `json:"destination_name,omitempty"`
	MirrorPath       string `json:"mirror_path"`
	MetadataPath     string `json:"metadata_path"`
	MirrorComplete   bool   `json:"mirror_complete"`
	MetadataComplete bool   `json:"metadata_complete"`
}

// Workspace owns the durable layout and repository identity mapping for one source forge.
type Workspace struct {
	root                 string
	index                workspaceIndex
	indexed              bool
	legacy               bool
	removeMetadataBackup func(string) error // Cleanup is injected so tests can prove it does not change an installed capture's outcome.
}

// Artifact identifies workspace-owned storage without deriving paths from display or destination names.
type Artifact struct {
	Identity         string
	Key              string
	MirrorPath       string
	MetadataPath     string
	MirrorComplete   bool
	MetadataComplete bool
}

// RescuedRepository associates a scanned repository with the artifact state available to later workflows.
type RescuedRepository struct {
	Repository      Repo
	Artifact        Artifact
	DestinationName string
}

// ArtifactFor returns the workspace-owned artifact location for a repository in the current scan.
func (w *Workspace) ArtifactFor(repo Repo) (Artifact, error) {
	scan, err := w.LoadScan()
	if err != nil {
		return Artifact{}, err
	}
	if err := w.ensureIndex(scan, false); err != nil {
		return Artifact{}, err
	}
	return w.artifact(repo)
}

// OpenWorkspace opens a rescue workspace and repairs any metadata swap interrupted after its first durable rename.
func OpenWorkspace(root string) (*Workspace, error) {
	if root == "" {
		root = "forge-rescue-data"
	}

	w := &Workspace{root: filepath.Clean(root), removeMetadataBackup: os.RemoveAll}
	indexPath := filepath.Join(w.root, "workspace.json")
	if err := readJSON(indexPath, &w.index); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			w.legacy = fileExists(filepath.Join(w.root, "scan.json"))
			return w, nil
		}
		return nil, fmt.Errorf("read workspace index: %w", err)
	}
	if err := validateIndex(w.index); err != nil {
		return nil, fmt.Errorf("validate workspace index: %w", err)
	}
	w.indexed = true
	if err := w.recoverMetadataSwaps(); err != nil {
		return nil, err
	}
	return w, nil
}

// SaveScan validates and atomically records a scan together with its collision-safe artifact index.
func (w *Workspace) SaveScan(scan Scan) error {
	if err := w.ensurePrivateDirectories(); err != nil {
		return err
	}
	if w.indexed && w.index.Instance != "" && scan.Instance != w.index.Instance {
		return fmt.Errorf("workspace belongs to %q, not %q", w.index.Instance, scan.Instance)
	}

	index, err := w.buildIndex(scan, w.legacy)
	if err != nil {
		return err
	}
	if err := writeJSONAtomic(filepath.Join(w.root, "scan.json"), scan); err != nil {
		return fmt.Errorf("write scan: %w", err)
	}
	if err := writeJSONAtomic(filepath.Join(w.root, "workspace.json"), index); err != nil {
		return fmt.Errorf("write workspace index: %w", err)
	}
	w.index = index
	w.indexed = true
	w.legacy = false
	return nil
}

// LoadScan recovers indexed metadata transactions before reading the workspace scan, while keeping legacy scans compatible.
func (w *Workspace) LoadScan() (Scan, error) {
	var scan Scan
	if w.indexed {
		if err := w.recoverMetadataSwaps(); err != nil {
			return scan, err
		}
	}
	if err := readJSON(filepath.Join(w.root, "scan.json"), &scan); err != nil {
		return scan, err
	}
	if w.indexed && w.index.Instance != "" && scan.Instance != w.index.Instance {
		return scan, fmt.Errorf("scan instance %q does not match workspace instance %q", scan.Instance, w.index.Instance)
	}
	return scan, nil
}

// SaveManifest atomically records the latest rescue run without exposing its filesystem path to callers.
func (w *Workspace) SaveManifest(manifest Manifest) error {
	if err := w.ensurePrivateDirectories(); err != nil {
		return err
	}
	return writeJSONAtomic(filepath.Join(w.root, "manifest.json"), manifest)
}

// LoadManifest recovers indexed metadata transactions before reading the latest rescue run.
func (w *Workspace) LoadManifest() (Manifest, error) {
	var manifest Manifest
	if w.indexed {
		if err := w.recoverMetadataSwaps(); err != nil {
			return manifest, err
		}
	}
	err := readJSON(filepath.Join(w.root, "manifest.json"), &manifest)
	return manifest, err
}

// SaveMetadata atomically replaces a repository's metadata directory only after every archival record is valid and writable.
func (w *Workspace) SaveMetadata(repo Repo, metadata RepositoryMetadata) error {
	scan, err := w.LoadScan()
	if err != nil {
		return err
	}
	if err := w.ensureIndex(scan, true); err != nil {
		return err
	}
	if err := validateMetadata(metadata); err != nil {
		return fmt.Errorf("validate metadata for %s: %w", repo.FullName, err)
	}
	artifact, err := w.artifact(repo)
	if err != nil {
		return err
	}
	if err := w.ensurePrivateDirectories(); err != nil {
		return err
	}
	if err := w.recoverMetadataSwap(repo.FullName, artifact.Identity, artifact.MetadataPath); err != nil {
		return err
	}

	parent := filepath.Dir(artifact.MetadataPath)
	staging, err := os.MkdirTemp(parent, ".metadata-")
	if err != nil {
		return fmt.Errorf("stage metadata for %s: %w", repo.FullName, err)
	}
	defer os.RemoveAll(staging)
	if err := os.Chmod(staging, 0o700); err != nil {
		return fmt.Errorf("protect staged metadata for %s: %w", repo.FullName, err)
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
		if err := writeJSONAtomic(filepath.Join(staging, record.name), record.value); err != nil {
			return fmt.Errorf("stage %s for %s: %w", record.name, repo.FullName, err)
		}
	}

	backup := w.metadataBackupPath(artifact.Identity)
	if pathExists(backup) {
		// A retained backup is harmless to the installed capture, but a new swap needs the exact backup path free.
		if err := w.removeBackup(backup); err != nil {
			return fmt.Errorf("prepare metadata replacement for %s: %w", repo.FullName, err)
		}
	}
	hadPrevious := directoryExists(artifact.MetadataPath)
	if hadPrevious {
		if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
			return fmt.Errorf("prepare metadata transaction for %s: %w", repo.FullName, err)
		}
		// The previous directory moves aside only after staging succeeds, so capture failures never damage the last complete archive.
		if err := os.Rename(artifact.MetadataPath, backup); err != nil {
			return fmt.Errorf("preserve previous metadata for %s: %w", repo.FullName, err)
		}
	}
	if err := os.Rename(staging, artifact.MetadataPath); err != nil {
		if hadPrevious {
			_ = os.Rename(backup, artifact.MetadataPath)
		}
		return fmt.Errorf("install metadata for %s: %w", repo.FullName, err)
	}
	if hadPrevious {
		// The canonical path now contains the complete new capture, so backup cleanup cannot invalidate the transaction.
		_ = w.removeBackup(backup)
	}
	return nil
}

// recoverMetadataSwaps repairs exact indexed metadata paths so opening a workspace always exposes a complete capture at its canonical path.
func (w *Workspace) recoverMetadataSwaps() error {
	for _, entry := range w.index.Repositories {
		metadataPath := w.artifactFromEntry(entry).MetadataPath
		if err := w.recoverMetadataSwap(entry.FullName, entry.Identity, metadataPath); err != nil {
			return err
		}
	}
	return nil
}

// recoverMetadataSwap restores a preserved capture after the first rename or discards it after the second rename completed.
func (w *Workspace) recoverMetadataSwap(fullName, identity, metadataPath string) error {
	backup := w.metadataBackupPath(identity)
	backupInfo, err := os.Lstat(backup)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect previous metadata for %s: %w", fullName, err)
	}
	_, currentErr := os.Lstat(metadataPath)
	if currentErr == nil {
		// Both paths mean the staged capture reached its canonical name, so the older capture is only cleanup residue.
		_ = w.removeBackup(backup)
		return nil
	}
	if !errors.Is(currentErr, os.ErrNotExist) {
		return fmt.Errorf("inspect metadata for %s: %w", fullName, currentErr)
	}
	if !backupInfo.IsDir() {
		return fmt.Errorf("previous metadata for %s is not a directory", fullName)
	}
	// Only the backup means interruption occurred between renames, so restoring it re-establishes the last complete capture.
	if err := os.Rename(backup, metadataPath); err != nil {
		return fmt.Errorf("restore previous metadata for %s: %w", fullName, err)
	}
	return nil
}

// removeBackup invokes the workspace's filesystem adapter so cleanup failure can be tested independently from capture installation.
func (w *Workspace) removeBackup(path string) error {
	if w.removeMetadataBackup == nil {
		return os.RemoveAll(path)
	}
	return w.removeMetadataBackup(path)
}

// metadataBackupPath hashes the validated source identity into a reserved namespace outside canonical metadata directories.
func (w *Workspace) metadataBackupPath(identity string) string {
	digest := sha256.Sum256([]byte(identity))
	return filepath.Join(w.root, ".metadata-backups", hex.EncodeToString(digest[:]))
}

// WriteReport atomically stores a named JSON report at the workspace root for downstream workflows.
func (w *Workspace) WriteReport(filename string, report any) error {
	if filepath.Base(filename) != filename || filepath.Ext(filename) != ".json" {
		return fmt.Errorf("report filename must be a root-level .json file: %q", filename)
	}
	if filename == "scan.json" || filename == "manifest.json" || filename == "workspace.json" {
		return fmt.Errorf("report filename is reserved by the workspace: %q", filename)
	}
	if err := w.ensurePrivateDirectories(); err != nil {
		return err
	}
	return writeJSONAtomic(filepath.Join(w.root, filename), report)
}

// Select validates explicit repository names before applying name or risk selection rules.
func (w *Workspace) Select(scan Scan, selection Selection, cfg RiskConfig, nowTime time.Time) ([]Repo, error) {
	if len(selection.Names) == 0 {
		return SelectRepos(scan, selection, cfg, nowTime), nil
	}

	available := make(map[string]struct{}, len(scan.Repos))
	for _, repo := range scan.Repos {
		available[repo.FullName] = struct{}{}
	}
	var unknown []string
	for _, name := range selection.Names {
		if _, ok := available[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("repositories not found in scan: %s", strings.Join(unknown, ", "))
	}
	return SelectRepos(scan, selection, cfg, nowTime), nil
}

// RescuedRepositories returns repositories whose mirror artifacts are present and complete.
func (w *Workspace) RescuedRepositories() ([]RescuedRepository, error) {
	return w.rescuedRepositories(false)
}

// UploadRepositories returns complete rescue artifacts after durably allocating collision-safe destination names.
func (w *Workspace) UploadRepositories() ([]RescuedRepository, error) {
	return w.rescuedRepositories(true)
}

// rescuedRepositories keeps read-only inspection side-effect free while upload persists names before remote mutations begin.
func (w *Workspace) rescuedRepositories(persistDestinations bool) ([]RescuedRepository, error) {
	scan, err := w.LoadScan()
	if err != nil {
		return nil, err
	}
	if err := w.ensureIndex(scan, false); err != nil {
		return nil, err
	}
	if err := w.ensureDestinationNames(scan, persistDestinations); err != nil {
		return nil, err
	}

	byName := make(map[string]Repo, len(scan.Repos))
	legacyGroups := make(map[string][]string, len(scan.Repos))
	for _, repo := range scan.Repos {
		byName[repo.FullName] = repo
		legacyName := SafeName(repo.FullName)
		legacyGroups[legacyName] = append(legacyGroups[legacyName], repo.FullName)
	}
	var rescued []RescuedRepository
	for _, entry := range w.index.Repositories {
		repo, ok := byName[entry.FullName]
		if !ok {
			continue
		}
		artifact := w.artifactFromEntry(entry)
		if !directoryExists(artifact.MirrorPath) {
			legacyName := SafeName(repo.FullName)
			legacyMirror := filepath.Join(w.root, "repos", legacyName+".git")
			if !directoryExists(legacyMirror) {
				continue
			}
			if len(legacyGroups[legacyName]) > 1 {
				names := append([]string(nil), legacyGroups[legacyName]...)
				sort.Strings(names)
				return nil, fmt.Errorf("legacy mirror %q is ambiguous for repositories %s", legacyName, strings.Join(names, ", "))
			}
			artifact.MirrorPath = legacyMirror // Unique flattened mirrors remain readable without moving user data.
			artifact.MirrorComplete = true
		}
		rescued = append(rescued, RescuedRepository{Repository: repo, Artifact: artifact, DestinationName: entry.DestinationName})
	}
	return rescued, nil
}

// ensureDestinationNames preserves prior allocations and assigns deterministic source-identity suffixes only where readable names collide.
func (w *Workspace) ensureDestinationNames(scan Scan, persist bool) error {
	byIdentity := make(map[string]Repo, len(scan.Repos))
	groups := make(map[string]int, len(scan.Repos))
	for _, repo := range scan.Repos {
		identity := repositoryIdentity(scan.Instance, repo)
		byIdentity[identity] = repo
		groups[strings.ToLower(readableDestinationName(repo.FullName))]++
	}

	used := make(map[string]string, len(w.index.Repositories))
	changed := false
	for _, entry := range w.index.Repositories {
		if entry.DestinationName == "" {
			continue
		}
		key := strings.ToLower(entry.DestinationName)
		if previous, exists := used[key]; exists && previous != entry.Identity {
			return fmt.Errorf("destination name %q is allocated to multiple source identities", entry.DestinationName)
		}
		used[key] = entry.Identity
	}

	order := make([]int, 0, len(w.index.Repositories))
	for i, entry := range w.index.Repositories {
		if entry.DestinationName == "" {
			order = append(order, i)
		}
	}
	sort.Slice(order, func(i, j int) bool {
		return w.index.Repositories[order[i]].Identity < w.index.Repositories[order[j]].Identity
	})
	for _, index := range order {
		entry := &w.index.Repositories[index]
		repo, ok := byIdentity[entry.Identity]
		if !ok {
			continue
		}
		base := readableDestinationName(repo.FullName)
		candidate := base
		if groups[strings.ToLower(base)] > 1 {
			candidate = destinationCandidate(base, destinationSuffix(w.index.Instance, repo))
		}
		if owner, exists := used[strings.ToLower(candidate)]; exists && owner != entry.Identity {
			candidate = destinationCandidate(base, destinationSuffix(w.index.Instance, repo))
		}
		for attempt := 2; ; attempt++ {
			key := strings.ToLower(candidate)
			if owner, exists := used[key]; !exists || owner == entry.Identity {
				used[key] = entry.Identity
				break
			}
			// A deterministic ordinal resolves the rare case where a readable name already ends in another repository's stable suffix.
			candidate = destinationCandidate(base, destinationSuffix(w.index.Instance, repo)+"-"+strconv.Itoa(attempt))
		}
		entry.DestinationName = candidate
		changed = true
	}
	if !changed || !persist {
		return nil
	}
	if err := w.ensurePrivateDirectories(); err != nil {
		return err
	}
	if err := writeJSONAtomic(filepath.Join(w.root, "workspace.json"), w.index); err != nil {
		return fmt.Errorf("persist destination names: %w", err)
	}
	return nil
}

// readableDestinationName converts a source address into GitHub-safe characters without reusing legacy artifact naming.
func readableDestinationName(fullName string) string {
	var name strings.Builder
	lastWasSeparator := false
	for _, character := range fullName {
		allowed := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("._-", character)
		if character == '/' || !allowed {
			if !lastWasSeparator {
				name.WriteByte('-')
				lastWasSeparator = true
			}
			continue
		}
		name.WriteRune(character)
		lastWasSeparator = false
	}
	return destinationCandidate(name.String(), "")
}

// destinationCandidate reserves space for a stable suffix while respecting GitHub's 100-character repository-name limit.
func destinationCandidate(base, suffix string) string {
	tail := ""
	if suffix != "" {
		tail = "-" + suffix
	}
	maxBaseLength := 100 - len(tail)
	if len(base) > maxBaseLength {
		base = base[:maxBaseLength]
	}
	return base + tail
}

// destinationSuffix uses the stable forge ID when available and a bounded legacy digest otherwise.
func destinationSuffix(instance string, repo Repo) string {
	if repo.ID != 0 {
		return strconv.FormatInt(repo.ID, 10)
	}
	return digestPrefix(legacyRepositoryDigest(instance, repo.FullName), 12)
}

// ensureIndex resolves legacy artifacts once and optionally persists the versioned mapping before a mutation.
func (w *Workspace) ensureIndex(scan Scan, persist bool) error {
	if w.indexed {
		if w.index.Instance != "" && scan.Instance != w.index.Instance {
			return fmt.Errorf("scan instance %q does not match workspace instance %q", scan.Instance, w.index.Instance)
		}
		return nil
	}

	index, err := w.buildIndex(scan, w.legacy)
	if err != nil {
		return err
	}
	w.index = index
	w.indexed = true
	if !persist {
		return nil
	}
	if err := w.ensurePrivateDirectories(); err != nil {
		return err
	}
	if err := writeJSONAtomic(filepath.Join(w.root, "workspace.json"), index); err != nil {
		return fmt.Errorf("write workspace index: %w", err)
	}
	return nil
}

// buildIndex validates source identities and maps each repository to either its unique legacy artifact or a new artifact key.
func (w *Workspace) buildIndex(scan Scan, inspectLegacy bool) (workspaceIndex, error) {
	index := workspaceIndex{Version: workspaceVersion, Instance: scan.Instance}
	seenIdentity := make(map[string]string, len(scan.Repos))
	seenName := make(map[string]int64, len(scan.Repos))
	legacyGroups := make(map[string][]Repo, len(scan.Repos))
	existing := make(map[string]workspaceRepository, len(w.index.Repositories))
	for _, entry := range w.index.Repositories {
		existing[entry.Identity] = entry
	}

	for _, repo := range scan.Repos {
		if err := validateFullName(repo.FullName); err != nil {
			return index, err
		}
		identity := repositoryIdentity(scan.Instance, repo)
		if previous, ok := seenIdentity[identity]; ok {
			return index, fmt.Errorf("source identity %q is duplicated by %q and %q", identity, previous, repo.FullName)
		}
		if previousID, ok := seenName[repo.FullName]; ok && previousID != repo.ID {
			return index, fmt.Errorf("repository name %q has conflicting source IDs %d and %d", repo.FullName, previousID, repo.ID)
		}
		seenIdentity[identity] = repo.FullName
		seenName[repo.FullName] = repo.ID
		legacyGroups[SafeName(repo.FullName)] = append(legacyGroups[SafeName(repo.FullName)], repo)
	}

	if inspectLegacy {
		for legacyName, repos := range legacyGroups {
			if len(repos) < 2 {
				continue
			}
			mirror := filepath.Join(w.root, "repos", legacyName+".git")
			metadata := filepath.Join(w.root, "metadata", legacyName)
			if directoryExists(mirror) || directoryExists(metadata) {
				names := make([]string, 0, len(repos))
				for _, repo := range repos {
					names = append(names, repo.FullName)
				}
				sort.Strings(names)
				return index, fmt.Errorf("legacy artifact %q is ambiguous for repositories %s", legacyName, strings.Join(names, ", "))
			}
		}
	}

	for _, repo := range scan.Repos {
		identity := repositoryIdentity(scan.Instance, repo)
		key := artifactKey(scan.Instance, repo)
		entry := workspaceRepository{
			ID:           repo.ID,
			FullName:     repo.FullName,
			Identity:     identity,
			ArtifactKey:  key,
			MirrorPath:   filepath.ToSlash(filepath.Join("repos", key+".git")),
			MetadataPath: filepath.ToSlash(filepath.Join("metadata", key)),
		}
		if previous, ok := existing[identity]; ok {
			entry.DestinationName = previous.DestinationName
			entry.MirrorPath = previous.MirrorPath
			entry.MetadataPath = previous.MetadataPath
			entry.MirrorComplete = previous.MirrorComplete
			entry.MetadataComplete = previous.MetadataComplete
		}

		if inspectLegacy && len(legacyGroups[SafeName(repo.FullName)]) == 1 {
			legacyMirror := filepath.Join(w.root, "repos", MirrorDir(repo.FullName))
			legacyMetadata := filepath.Join(w.root, "metadata", SafeName(repo.FullName))
			if directoryExists(legacyMirror) {
				entry.MirrorPath = filepath.ToSlash(filepath.Join("repos", MirrorDir(repo.FullName)))
				entry.MirrorComplete = true
			}
			if directoryExists(legacyMetadata) {
				entry.MetadataPath = filepath.ToSlash(filepath.Join("metadata", SafeName(repo.FullName)))
				entry.MetadataComplete = metadataDirectoryComplete(legacyMetadata)
			}
		}
		index.Repositories = append(index.Repositories, entry)
	}
	return index, nil
}

// artifact returns the indexed storage and completion state for a scanned repository.
func (w *Workspace) artifact(repo Repo) (Artifact, error) {
	for _, entry := range w.index.Repositories {
		if entry.Identity == repositoryIdentity(w.index.Instance, repo) {
			return w.artifactFromEntry(entry), nil
		}
	}
	return Artifact{}, fmt.Errorf("repository %q is not indexed in this workspace", repo.FullName)
}

// artifactFromEntry resolves stored relative paths beneath the workspace root.
func (w *Workspace) artifactFromEntry(entry workspaceRepository) Artifact {
	return Artifact{
		Identity:         entry.Identity,
		Key:              entry.ArtifactKey,
		MirrorPath:       filepath.Join(w.root, filepath.FromSlash(entry.MirrorPath)),
		MetadataPath:     filepath.Join(w.root, filepath.FromSlash(entry.MetadataPath)),
		MirrorComplete:   entry.MirrorComplete,
		MetadataComplete: entry.MetadataComplete,
	}
}

// markPhase records completed phases immediately so an interrupted rescue can resume without recloning.
func (w *Workspace) markPhase(identity string, mirrorComplete, metadataComplete bool) error {
	for i := range w.index.Repositories {
		entry := &w.index.Repositories[i]
		if entry.Identity != identity {
			continue
		}
		entry.MirrorComplete = entry.MirrorComplete || mirrorComplete
		entry.MetadataComplete = entry.MetadataComplete || metadataComplete
		return writeJSONAtomic(filepath.Join(w.root, "workspace.json"), w.index)
	}
	return fmt.Errorf("source identity %q is not indexed in this workspace", identity)
}

// ensurePrivateDirectories creates workspace-owned directories with owner-only permissions.
func (w *Workspace) ensurePrivateDirectories() error {
	for _, path := range []string{w.root, filepath.Join(w.root, "repos"), filepath.Join(w.root, "metadata")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create workspace directory %s: %w", path, err)
		}
		if err := os.Chmod(path, 0o700); err != nil {
			return fmt.Errorf("protect workspace directory %s: %w", path, err)
		}
	}
	return nil
}

// repositoryIdentity uses the provider ID when present and an exact instance/name digest only for legacy zero-ID scans.
func repositoryIdentity(instance string, repo Repo) string {
	if repo.ID != 0 {
		return "gitea:" + strconv.FormatInt(repo.ID, 10)
	}
	return "legacy:" + legacyRepositoryDigest(instance, repo.FullName)
}

// artifactKey keeps local storage identity independent from human-readable and destination repository names.
func artifactKey(instance string, repo Repo) string {
	if repo.ID != 0 {
		return "repo-" + strconv.FormatInt(repo.ID, 10)
	}
	return "legacy-" + legacyRepositoryDigest(instance, repo.FullName)
}

// legacyRepositoryDigest centralizes zero-ID identity derivation so every caller uses the same source inputs.
func legacyRepositoryDigest(instance, fullName string) string {
	digest := sha256.Sum256([]byte(instance + "\x00" + fullName))
	return hex.EncodeToString(digest[:])
}

// digestPrefix bounds readable suffixes without assuming a digest is longer than the requested prefix.
func digestPrefix(digest string, length int) string {
	if length <= 0 {
		return ""
	}
	if len(digest) <= length {
		return digest
	}
	return digest[:length]
}

// validateFullName rejects names that cannot identify exactly one owner and repository.
func validateFullName(fullName string) error {
	owner, name, ok := strings.Cut(fullName, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return fmt.Errorf("repository full name must be exactly owner/name: %q", fullName)
	}
	return nil
}

// validateIndex rejects unsupported or internally inconsistent workspace metadata before callers use its paths.
func validateIndex(index workspaceIndex) error {
	if index.Version != workspaceVersion {
		return fmt.Errorf("unsupported workspace version %d", index.Version)
	}
	seen := make(map[string]string, len(index.Repositories))
	seenDestination := make(map[string]string, len(index.Repositories))
	for _, entry := range index.Repositories {
		if err := validateFullName(entry.FullName); err != nil {
			return err
		}
		if entry.Identity == "" || entry.ArtifactKey == "" {
			return fmt.Errorf("repository %q has an incomplete workspace identity", entry.FullName)
		}
		if previous, ok := seen[entry.Identity]; ok && previous != entry.FullName {
			return fmt.Errorf("source identity %q belongs to both %q and %q", entry.Identity, previous, entry.FullName)
		}
		if err := validateArtifactPath(entry.FullName, entry.MirrorPath, "repos"); err != nil {
			return err
		}
		if err := validateArtifactPath(entry.FullName, entry.MetadataPath, "metadata"); err != nil {
			return err
		}
		if entry.DestinationName != "" {
			if strings.Contains(entry.DestinationName, "/") || strings.TrimSpace(entry.DestinationName) != entry.DestinationName {
				return fmt.Errorf("repository %q has an unsafe destination name %q", entry.FullName, entry.DestinationName)
			}
			key := strings.ToLower(entry.DestinationName)
			if previous, ok := seenDestination[key]; ok && previous != entry.Identity {
				return fmt.Errorf("destination name %q belongs to multiple source identities", entry.DestinationName)
			}
			seenDestination[key] = entry.Identity
		}
		seen[entry.Identity] = entry.FullName
	}
	return nil
}

// validateArtifactPath confines an indexed artifact to its workspace-owned directory.
func validateArtifactPath(fullName, relativePath, directory string) error {
	clean := filepath.Clean(filepath.FromSlash(relativePath))
	wantedPrefix := directory + string(filepath.Separator)
	if filepath.IsAbs(clean) || !strings.HasPrefix(clean, wantedPrefix) || clean == wantedPrefix {
		return fmt.Errorf("repository %q has an unsafe artifact path %q", fullName, relativePath)
	}
	return nil
}

// directoryExists reports whether a path currently names a directory.
func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// pathExists reports whether an exact transaction path exists without following a final symlink.
func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// fileExists reports whether a path currently names a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// metadataDirectoryComplete recognizes a legacy capture only when all four records decode into their expected JSON shapes.
func metadataDirectoryComplete(path string) bool {
	var metadata RepositoryMetadata
	if err := readJSON(filepath.Join(path, "repo.json"), &metadata.Repository); err != nil {
		return false
	}
	collections := []struct {
		name   string
		target *[]json.RawMessage
	}{
		{name: "issues.json", target: &metadata.Issues},
		{name: "releases.json", target: &metadata.Releases},
		{name: "labels.json", target: &metadata.Labels},
	}
	for _, collection := range collections {
		if err := readJSON(filepath.Join(path, collection.name), collection.target); err != nil {
			return false
		}
	}
	return validateMetadata(metadata) == nil
}

// validateMetadata rejects incomplete or malformed captures before SaveMetadata changes durable workspace state.
func validateMetadata(metadata RepositoryMetadata) error {
	var repository map[string]json.RawMessage
	if !json.Valid(metadata.Repository) || json.Unmarshal(metadata.Repository, &repository) != nil || repository == nil {
		return errors.New("repository record is missing or is not a JSON object")
	}
	collections := []struct {
		name  string
		items []json.RawMessage
	}{
		{name: "issues", items: metadata.Issues},
		{name: "releases", items: metadata.Releases},
		{name: "labels", items: metadata.Labels},
	}
	for _, collection := range collections {
		if collection.items == nil {
			return fmt.Errorf("%s collection is missing", collection.name)
		}
		for index, item := range collection.items {
			if !json.Valid(item) {
				return fmt.Errorf("%s item %d is invalid JSON", collection.name, index)
			}
		}
	}
	return nil
}
