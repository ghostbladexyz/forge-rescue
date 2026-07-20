package gitea

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/ghostbladexyz/forge-rescue/internal/rescue"
)

// CaptureMetadata returns one complete in-memory archive so callers persist nothing after a partial remote failure.
func (s *Source) CaptureMetadata(ctx context.Context, repo rescue.Repo) (rescue.RepositoryMetadata, error) {
	if err := validateRepositoryName(repo.FullName); err != nil {
		return rescue.RepositoryMetadata{}, err
	}
	owner, name, _ := strings.Cut(repo.FullName, "/")
	basePath := "/api/v1/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)

	var repository json.RawMessage
	if _, err := s.getJSON(ctx, basePath, nil, &repository); err != nil {
		return rescue.RepositoryMetadata{}, fmt.Errorf("capture repository record for %s: %w", repo.FullName, err)
	}
	issues, err := s.captureCollection(ctx, basePath+"/issues")
	if err != nil {
		return rescue.RepositoryMetadata{}, fmt.Errorf("capture issues for %s: %w", repo.FullName, err)
	}
	releases, err := s.captureCollection(ctx, basePath+"/releases")
	if err != nil {
		return rescue.RepositoryMetadata{}, fmt.Errorf("capture releases for %s: %w", repo.FullName, err)
	}
	labels, err := s.captureCollection(ctx, basePath+"/labels")
	if err != nil {
		return rescue.RepositoryMetadata{}, fmt.Errorf("capture labels for %s: %w", repo.FullName, err)
	}

	return rescue.RepositoryMetadata{
		Repository: repository,
		Issues:     issues,
		Releases:   releases,
		Labels:     labels,
	}, nil
}

// captureCollection removes overlap between changing pages while preserving the first-seen archival order.
func (s *Source) captureCollection(ctx context.Context, path string) ([]json.RawMessage, error) {
	items, err := fetchPages[json.RawMessage](ctx, s, path)
	if err != nil {
		return nil, err
	}
	unique := make([]json.RawMessage, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		identity, err := metadataIdentity(item)
		if err != nil {
			return nil, fmt.Errorf("identify item from %s: %w", path, err)
		}
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		unique = append(unique, item)
	}
	return unique, nil
}

// metadataIdentity prefers Gitea's stable numeric ID and hashes canonical JSON only for legacy records without one.
func metadataIdentity(raw json.RawMessage) (string, error) {
	var identity struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(raw, &identity); err != nil {
		return "", err
	}
	if len(identity.ID) > 0 && string(identity.ID) != "null" && string(identity.ID) != "0" {
		var id int64
		if err := json.Unmarshal(identity.ID, &id); err != nil || id <= 0 {
			return "", fmt.Errorf("metadata id must be a positive integer: %s", identity.ID)
		}
		return fmt.Sprintf("id:%d", id), nil
	}
	compact, err := compactJSON(raw)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(compact)
	return "json:" + hex.EncodeToString(digest[:]), nil
}
