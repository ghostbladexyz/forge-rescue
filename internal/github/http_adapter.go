package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	githubAPIVersion = "2026-03-10"
	maxErrorBody     = 64 * 1024
	refsRetryDelay   = 100 * time.Millisecond
)

type httpAdapter struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

type githubError struct {
	Operation        string
	StatusCode       int
	Status           string
	Message          string
	DocumentationURL string
}

type errorResponse struct {
	Message          string `json:"message"`
	DocumentationURL string `json:"documentation_url"`
}

// newHTTPAdapter creates the production REST adapter while allowing package tests to supply a local transport.
func newHTTPAdapter(baseURL, token string, client *http.Client) *httpAdapter {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &httpAdapter{baseURL: strings.TrimRight(baseURL, "/"), token: token, httpClient: client}
}

// Error presents bounded GitHub context without exposing request credentials or arbitrary response bodies.
func (e *githubError) Error() string {
	message := e.Operation + " returned " + e.Status
	if e.Message != "" {
		message += ": " + e.Message
	}
	if e.DocumentationURL != "" {
		message += " (" + e.DocumentationURL + ")"
	}
	return message
}

// AuthenticatedUser resolves the login whose personal repository endpoint the token may use.
func (a *httpAdapter) AuthenticatedUser(ctx context.Context) (string, error) {
	response, err := a.do(ctx, http.MethodGet, "/user", nil)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", a.responseError("get authenticated GitHub user", response)
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(response.Body).Decode(&user); err != nil {
		return "", fmt.Errorf("decode authenticated GitHub user: %w", err)
	}
	if user.Login == "" {
		return "", fmt.Errorf("authenticated GitHub user response omitted login")
	}
	return user.Login, nil
}

// RepositoryExists distinguishes an absent destination from all authorization and transport failures.
func (a *httpAdapter) RepositoryExists(ctx context.Context, owner, name string) (bool, error) {
	response, err := a.do(ctx, http.MethodGet, repositoryPath(owner, name), nil)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, a.responseError("get GitHub repository", response)
	}
}

// CreateUserRepository creates a private uninitialized repository for the authenticated user.
func (a *httpAdapter) CreateUserRepository(ctx context.Context, name string) error {
	return a.createRepository(ctx, "/user/repos", name)
}

// CreateOrganizationRepository creates a private uninitialized repository under the selected organization.
func (a *httpAdapter) CreateOrganizationRepository(ctx context.Context, owner, name string) error {
	return a.createRepository(ctx, "/orgs/"+url.PathEscape(owner)+"/repos", name)
}

// createRepository requires GitHub's exact creation status so redirects or partial responses never count as success.
func (a *httpAdapter) createRepository(ctx context.Context, path, name string) error {
	body := struct {
		Name     string `json:"name"`
		Private  bool   `json:"private"`
		AutoInit bool   `json:"auto_init"`
	}{Name: name, Private: true, AutoInit: false}
	response, err := a.do(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return a.responseError("create GitHub repository", response)
	}
	return nil
}

// HasRefs checks every Git reference and retries one conflict because GitHub also uses 409 while a new repository is becoming available.
func (a *httpAdapter) HasRefs(ctx context.Context, owner, name string) (bool, error) {
	path := repositoryPath(owner, name) + "/git/matching-refs/"
	for attempt := 0; attempt < 2; attempt++ {
		response, err := a.do(ctx, http.MethodGet, path, nil)
		if err != nil {
			return false, err
		}
		if response.StatusCode == http.StatusConflict {
			response.Body.Close()
			if attempt == 0 {
				timer := time.NewTimer(refsRetryDelay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return false, ctx.Err()
				case <-timer.C:
				}
				continue
			}
			// Two 409 responses are treated as GitHub's empty-repository state; the retry filters the documented transient alternative.
			return false, nil
		}
		if response.StatusCode != http.StatusOK {
			err := a.responseError("list GitHub repository refs", response)
			response.Body.Close()
			return false, err
		}
		var refs []json.RawMessage
		err = json.NewDecoder(response.Body).Decode(&refs)
		response.Body.Close()
		if err != nil {
			return false, fmt.Errorf("decode GitHub repository refs: %w", err)
		}
		return len(refs) > 0, nil
	}
	return false, nil
}

// DeleteRepository returns false only for GitHub's deliberately ambiguous not-found response.
func (a *httpAdapter) DeleteRepository(ctx context.Context, owner, name string) (bool, error) {
	response, err := a.do(ctx, http.MethodDelete, repositoryPath(owner, name), nil)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusNoContent:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, a.responseError("delete GitHub repository", response)
	}
}

// do attaches pinned representation and version headers to every GitHub request.
func (a *httpAdapter) do(ctx context.Context, method, path string, value any) (*http.Response, error) {
	var body io.Reader
	if value != nil {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode GitHub request: %w", err)
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("build GitHub request: %w", err)
	}
	if a.token != "" {
		request.Header.Set("Authorization", "Bearer "+a.token)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	if value != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := a.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send GitHub request: %w", err)
	}
	return response, nil
}

// responseError decodes only GitHub's bounded structured fields so reports stay useful and safe.
func (a *httpAdapter) responseError(operation string, response *http.Response) error {
	limited := io.LimitReader(response.Body, maxErrorBody)
	var payload errorResponse
	_ = json.NewDecoder(limited).Decode(&payload)
	return &githubError{
		Operation:        operation,
		StatusCode:       response.StatusCode,
		Status:           response.Status,
		Message:          a.safeResponseField(payload.Message),
		DocumentationURL: a.safeResponseField(payload.DocumentationURL),
	}
}

// safeResponseField redacts credentials if a proxy or remote error unexpectedly echoes request data.
func (a *httpAdapter) safeResponseField(value string) string {
	if a.token == "" {
		return value
	}
	return strings.ReplaceAll(value, a.token, "[redacted]")
}

// repositoryPath escapes owner and repository segments independently so neither can alter routing.
func repositoryPath(owner, name string) string {
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}
