package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// TestHTTPAdapterRoutesUserAndOrganizationCreation verifies endpoint choice, pinned headers, and private uninitialized payloads.
func TestHTTPAdapterRoutesUserAndOrganizationCreation(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("X-GitHub-Api-Version") != githubAPIVersion {
			t.Fatalf("GitHub headers = %#v", r.Header)
		}
		if r.Method == http.MethodGet {
			writeAdapterJSON(t, w, http.StatusOK, map[string]string{"login": "Alice"})
			return
		}
		var body struct {
			Name     string `json:"name"`
			Private  bool   `json:"private"`
			AutoInit bool   `json:"auto_init"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decoding creation body: %v", err)
		}
		if body.Name != "team-project" || !body.Private || body.AutoInit {
			t.Fatalf("creation body = %#v", body)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	adapter := newHTTPAdapter(server.URL, "token", server.Client())

	login, err := adapter.AuthenticatedUser(context.Background())
	if err != nil || login != "Alice" {
		t.Fatalf("AuthenticatedUser = %q, %v", login, err)
	}
	if err := adapter.CreateUserRepository(context.Background(), "team-project"); err != nil {
		t.Fatalf("CreateUserRepository returned error: %v", err)
	}
	if err := adapter.CreateOrganizationRepository(context.Background(), "rescue-org", "team-project"); err != nil {
		t.Fatalf("CreateOrganizationRepository returned error: %v", err)
	}
	want := []string{"GET /user", "POST /user/repos", "POST /orgs/rescue-org/repos"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %#v, want %#v", paths, want)
	}
}

// TestHTTPAdapterHandlesRepositoryStates verifies exact status handling for existence, refs, and deletion.
func TestHTTPAdapterHandlesRepositoryStates(t *testing.T) {
	refsCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/alice/present":
			writeAdapterJSON(t, w, http.StatusOK, map[string]any{})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/alice/missing":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/repos/alice/empty/git/matching-refs/":
			refsCalls++
			w.WriteHeader(http.StatusConflict)
		case r.Method == http.MethodGet && r.URL.Path == "/repos/alice/full/git/matching-refs/":
			writeAdapterJSON(t, w, http.StatusOK, []map[string]string{{"ref": "refs/heads/main"}})
		case r.Method == http.MethodDelete && r.URL.Path == "/repos/alice/present":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/repos/alice/missing":
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	adapter := newHTTPAdapter(server.URL, "token", server.Client())

	if exists, err := adapter.RepositoryExists(context.Background(), "alice", "present"); err != nil || !exists {
		t.Fatalf("present RepositoryExists = %v, %v", exists, err)
	}
	if exists, err := adapter.RepositoryExists(context.Background(), "alice", "missing"); err != nil || exists {
		t.Fatalf("missing RepositoryExists = %v, %v", exists, err)
	}
	if refs, err := adapter.HasRefs(context.Background(), "alice", "empty"); err != nil || refs || refsCalls != 2 {
		t.Fatalf("empty HasRefs = %v, %v, calls %d", refs, err, refsCalls)
	}
	if refs, err := adapter.HasRefs(context.Background(), "alice", "full"); err != nil || !refs {
		t.Fatalf("full HasRefs = %v, %v", refs, err)
	}
	if deleted, err := adapter.DeleteRepository(context.Background(), "alice", "present"); err != nil || !deleted {
		t.Fatalf("present DeleteRepository = %v, %v", deleted, err)
	}
	if deleted, err := adapter.DeleteRepository(context.Background(), "alice", "missing"); err != nil || deleted {
		t.Fatalf("missing DeleteRepository = %v, %v", deleted, err)
	}
}

// TestHTTPAdapterReturnsBoundedStructuredErrors verifies GitHub diagnostics survive without returning raw response bodies.
func TestHTTPAdapterReturnsBoundedStructuredErrors(t *testing.T) {
	token := "secret-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeAdapterJSON(t, w, http.StatusForbidden, map[string]string{
			"message":           "organization policy blocks deletion for " + token,
			"documentation_url": "https://docs.github.com/example",
		})
	}))
	defer server.Close()
	adapter := newHTTPAdapter(server.URL, token, server.Client())

	_, err := adapter.DeleteRepository(context.Background(), "alice", "project")
	var githubErr *githubError
	if !errors.As(err, &githubErr) || githubErr.StatusCode != http.StatusForbidden || strings.Contains(err.Error(), token) || !strings.Contains(githubErr.Message, "[redacted]") {
		t.Fatalf("DeleteRepository error = %#v", err)
	}
}

// writeAdapterJSON emits one deterministic GitHub response fixture.
func writeAdapterJSON(t *testing.T, w http.ResponseWriter, status int, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encoding response: %v", err)
	}
}
