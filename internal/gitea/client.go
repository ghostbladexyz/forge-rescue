package gitea

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ghostbladexyz/forge-rescue/internal/rescue"
)

const (
	pageSize         = 50
	maxErrorBodySize = 4 << 10
)

// Source captures repositories and archival metadata from one authenticated Gitea instance.
type Source struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

type organization struct {
	UserName string `json:"username"`
	Name     string `json:"name"`
}

// NewSource validates an instance URL once so every later request can use a trusted token-free base URL.
func NewSource(instanceURL, token string) (*Source, error) {
	parsed, err := url.Parse(instanceURL)
	if err != nil {
		return nil, errors.New("invalid Gitea instance URL")
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("Gitea instance URL must be an absolute HTTP(S) URL")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return nil, fmt.Errorf("Gitea instance URL must not contain credentials, a query, or a fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""

	return &Source{
		baseURL: parsed.String(),
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// Discover returns a deterministic repository catalog spanning the current user and their organizations.
func (s *Source) Discover(ctx context.Context) ([]rescue.Repo, error) {
	repositories, err := fetchPages[rescue.Repo](ctx, s, "/api/v1/user/repos")
	if err != nil {
		return nil, fmt.Errorf("discover user repositories: %w", err)
	}
	organizations, err := fetchPages[organization](ctx, s, "/api/v1/user/orgs")
	if err != nil {
		return nil, fmt.Errorf("discover organizations: %w", err)
	}

	organizationNames := make(map[string]struct{}, len(organizations))
	for _, org := range organizations {
		name := org.UserName
		if name == "" {
			name = org.Name // Older Gitea responses may expose the organization login as name instead of username.
		}
		if name == "" {
			return nil, errors.New("discover organizations: response contains an organization without a username or name")
		}
		organizationNames[name] = struct{}{}
	}
	orderedOrganizations := make([]string, 0, len(organizationNames))
	for name := range organizationNames {
		orderedOrganizations = append(orderedOrganizations, name)
	}
	sort.Strings(orderedOrganizations)

	for _, name := range orderedOrganizations {
		path := "/api/v1/orgs/" + url.PathEscape(name) + "/repos"
		organizationRepositories, err := fetchPages[rescue.Repo](ctx, s, path)
		if err != nil {
			return nil, fmt.Errorf("discover repositories for organization %s: %w", name, err)
		}
		repositories = append(repositories, organizationRepositories...)
	}
	return normalizeRepositories(repositories)
}

// getJSON performs one authenticated request and reports only token-free request context on failure.
func (s *Source) getJSON(ctx context.Context, path string, query url.Values, target any) (http.Header, error) {
	endpoint := s.baseURL + path
	displayPath := path
	if len(query) > 0 {
		encoded := query.Encode()
		endpoint += "?" + encoded
		displayPath += "?" + encoded
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build GET %s: %w", displayPath, err)
	}
	if s.token != "" {
		request.Header.Set("Authorization", "token "+s.token)
	}
	request.Header.Set("Accept", "application/json")

	response, err := s.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", displayPath, err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBodySize+1))
		return nil, fmt.Errorf("GET %s returned %s%s", displayPath, response.Status, s.safeErrorExcerpt(body))
	}

	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(target); err != nil {
		return nil, fmt.Errorf("decode GET %s: %w", displayPath, err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return nil, fmt.Errorf("decode GET %s: %w", displayPath, err)
	}
	return response.Header.Clone(), nil
}

// safeErrorExcerpt bounds and redacts remote error text because a forge may echo request data in its response.
func (s *Source) safeErrorExcerpt(body []byte) string {
	truncated := len(body) > maxErrorBodySize
	if truncated {
		body = body[:maxErrorBodySize]
	}
	excerpt := strings.Join(strings.Fields(string(body)), " ")
	if s.token != "" {
		excerpt = strings.ReplaceAll(excerpt, s.token, "[redacted]")
	}
	if excerpt == "" {
		return ""
	}
	if truncated {
		excerpt += "…"
	}
	return ": " + excerpt
}

// fetchPages gathers one list endpoint while honoring Gitea pagination headers and guarding ignored page parameters.
func fetchPages[T any](ctx context.Context, source *Source, path string) ([]T, error) {
	all := make([]T, 0)
	seenPages := make(map[string]int)
	for page := 1; ; page++ {
		query := url.Values{"limit": {strconv.Itoa(pageSize)}, "page": {strconv.Itoa(page)}}
		var items []T
		headers, err := source.getJSON(ctx, path, query, &items)
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			return all, nil
		}

		fingerprint, err := pageFingerprint(items)
		if err != nil {
			return nil, fmt.Errorf("fingerprint page %d from %s: %w", page, path, err)
		}
		if previous, exists := seenPages[fingerprint]; exists {
			return nil, fmt.Errorf("GET %s repeated page %d as page %d; the instance may ignore pagination", path, previous, page)
		}
		seenPages[fingerprint] = page
		all = append(all, items...)

		link := headers.Get("Link")
		if link != "" {
			if !hasNextLink(link) {
				return all, nil
			}
			continue
		}
		if totalText := headers.Get("X-Total-Count"); totalText != "" {
			total, err := strconv.Atoi(totalText)
			if err != nil || total < 0 {
				return nil, fmt.Errorf("GET %s returned invalid X-Total-Count %q", path, totalText)
			}
			if len(all) >= total {
				return all, nil
			}
		}
		// Headerless older instances require the next request because they may enforce a page size below the requested limit.
	}
}

// hasNextLink reports whether Gitea advertised another page without trusting a remote absolute URL for the next request.
func hasNextLink(header string) bool {
	for _, link := range strings.Split(header, ",") {
		for _, parameter := range strings.Split(link, ";")[1:] {
			name, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if !ok || !strings.EqualFold(name, "rel") {
				continue
			}
			for _, relation := range strings.Fields(strings.Trim(value, `"`)) {
				if relation == "next" {
					return true
				}
			}
		}
	}
	return false
}

// pageFingerprint identifies repeated pages by decoded content so a server that ignores page cannot loop forever.
func pageFingerprint[T any](items []T) (string, error) {
	encoded, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// normalizeRepositories deduplicates overlapping discovery paths and rejects source identity conflicts before sorting.
func normalizeRepositories(repositories []rescue.Repo) ([]rescue.Repo, error) {
	byName := make(map[string]rescue.Repo, len(repositories))
	nameByID := make(map[int64]string, len(repositories))
	for _, repository := range repositories {
		if err := validateRepositoryName(repository.FullName); err != nil {
			return nil, err
		}
		if repository.ID != 0 {
			if previousName, exists := nameByID[repository.ID]; exists && previousName != repository.FullName {
				return nil, fmt.Errorf("Gitea repository ID %d belongs to both %q and %q", repository.ID, previousName, repository.FullName)
			}
			nameByID[repository.ID] = repository.FullName
		}
		if previous, exists := byName[repository.FullName]; exists {
			if previous.ID != 0 && repository.ID != 0 && previous.ID != repository.ID {
				return nil, fmt.Errorf("Gitea repository %q has conflicting IDs %d and %d", repository.FullName, previous.ID, repository.ID)
			}
			if previous.ID == 0 && repository.ID != 0 {
				byName[repository.FullName] = repository // Prefer stable identity when another discovery path omitted the ID.
			}
			continue
		}
		byName[repository.FullName] = repository
	}

	normalized := make([]rescue.Repo, 0, len(byName))
	for _, repository := range byName {
		normalized = append(normalized, repository)
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].FullName == normalized[j].FullName {
			return normalized[i].ID < normalized[j].ID
		}
		return normalized[i].FullName < normalized[j].FullName
	})
	return normalized, nil
}

// validateRepositoryName requires exactly one nonempty owner/name pair so escaped endpoint paths stay unambiguous.
func validateRepositoryName(fullName string) error {
	owner, name, ok := strings.Cut(fullName, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return fmt.Errorf("repository full name must be exactly owner/name: %q", fullName)
	}
	return nil
}

// compactJSON returns a stable representation used only when a metadata item lacks a source ID.
func compactJSON(raw json.RawMessage) ([]byte, error) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, err
	}
	return compact.Bytes(), nil
}
