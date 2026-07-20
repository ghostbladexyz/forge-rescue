package gitea

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDiscoverPaginatesDeduplicatesAndSorts verifies every discovery path contributes once to a stable catalog.
func TestDiscoverPaginatesDeduplicatesAndSorts(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "token test-token" {
			t.Errorf("Authorization = %q, want token header", r.Header.Get("Authorization"))
		}
		page := r.URL.Query().Get("page")
		switch r.URL.Path {
		case "/api/v1/user/repos":
			if page == "1" {
				writeNextPage(w, r)
				writeJSON(t, w, []map[string]any{{"id": 2, "full_name": "team/shared"}})
				return
			}
			writeLastPage(w)
			writeJSON(t, w, []map[string]any{{"id": 1, "full_name": "alice/app"}})
		case "/api/v1/user/orgs":
			if page == "1" {
				writeNextPage(w, r)
				writeJSON(t, w, []map[string]any{{"username": "team"}})
				return
			}
			writeLastPage(w)
			writeJSON(t, w, []map[string]any{{"name": "lab"}})
		case "/api/v1/orgs/lab/repos":
			writeLastPage(w)
			writeJSON(t, w, []map[string]any{{"id": 4, "full_name": "lab/tool"}})
		case "/api/v1/orgs/team/repos":
			writeLastPage(w)
			writeJSON(t, w, []map[string]any{
				{"id": 3, "full_name": "team/lib"},
				{"id": 2, "full_name": "team/shared"},
			})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	source, err := NewSource(server.URL, "test-token")
	if err != nil {
		t.Fatalf("NewSource returned error: %v", err)
	}
	repositories, err := source.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	want := []string{"alice/app", "lab/tool", "team/lib", "team/shared"}
	if len(repositories) != len(want) {
		t.Fatalf("repository count = %d, want %d", len(repositories), len(want))
	}
	for index, fullName := range want {
		if repositories[index].FullName != fullName {
			t.Fatalf("repositories = %#v, want sorted names %#v", repositories, want)
		}
	}
	if requests != 6 {
		t.Fatalf("request count = %d, want 6 paginated requests", requests)
	}
}

// TestDiscoverRejectsIdentityConflicts verifies duplicate source IDs cannot silently identify different repositories.
func TestDiscoverRejectsIdentityConflicts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/user/repos":
			writeLastPage(w)
			writeJSON(t, w, []map[string]any{{"id": 7, "full_name": "alice/one"}, {"id": 7, "full_name": "alice/two"}})
		case "/api/v1/user/orgs":
			writeLastPage(w)
			writeJSON(t, w, []map[string]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	source, err := NewSource(server.URL, "token")
	if err != nil {
		t.Fatalf("NewSource returned error: %v", err)
	}
	_, err = source.Discover(context.Background())
	if err == nil || !strings.Contains(err.Error(), "belongs to both") {
		t.Fatalf("Discover error = %v, want conflicting identity context", err)
	}
}

// TestDiscoverRejectsRepeatedHeaderlessPage verifies ignored page parameters fail instead of looping forever.
func TestDiscoverRejectsRepeatedHeaderlessPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/user/repos" {
			writeJSON(t, w, []map[string]any{{"id": 1, "full_name": "alice/app"}})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	source, err := NewSource(server.URL, "token")
	if err != nil {
		t.Fatalf("NewSource returned error: %v", err)
	}
	_, err = source.Discover(context.Background())
	if err == nil || !strings.Contains(err.Error(), "repeated page 1 as page 2") {
		t.Fatalf("Discover error = %v, want repeated-page protection", err)
	}
}

// TestNewSourceRejectsUnsafeInstanceURLs verifies credentials and ambiguous URL suffixes never enter request construction.
func TestNewSourceRejectsUnsafeInstanceURLs(t *testing.T) {
	for _, instanceURL := range []string{"git.example", "ftp://git.example", "https://user:secret@git.example", "https://git.example?token=secret", "https://git.example#fragment"} {
		if _, err := NewSource(instanceURL, "token"); err == nil {
			t.Errorf("NewSource(%q) returned nil error", instanceURL)
		}
	}
}

// TestSourceErrorsAreBoundedAndTokenFree verifies hostile remote text cannot leak credentials or create unbounded diagnostics.
func TestSourceErrorsAreBoundedAndTokenFree(t *testing.T) {
	const token = "secret-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(token + strings.Repeat("x", maxErrorBodySize*2)))
	}))
	defer server.Close()

	source, err := NewSource(server.URL, token)
	if err != nil {
		t.Fatalf("NewSource returned error: %v", err)
	}
	_, err = source.Discover(context.Background())
	if err == nil || strings.Contains(err.Error(), token) || len(err.Error()) > maxErrorBodySize+300 {
		t.Fatalf("Discover error is not safely bounded/redacted: length=%d error=%v", len(err.Error()), err)
	}
}

// writeNextPage advertises page two because Source follows Gitea's Link pagination contract.
func writeNextPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Link", "<"+r.URL.Path+"?page=2>; rel=\"next\"")
}

// writeLastPage supplies a non-next Link header so a nonempty fixture page is unambiguously terminal.
func writeLastPage(w http.ResponseWriter) {
	w.Header().Set("Link", `</previous>; rel="prev"`)
}

// writeJSON encodes one HTTP fixture response and reports failures against the active test.
func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encoding JSON: %v", err)
	}
}
