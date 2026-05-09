package gitea

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListRepositoriesIncludesUserAndOrgReposWithPagination(t *testing.T) {
	var seenAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "token test-token" {
			seenAuth = true
		}

		switch r.URL.Path {
		case "/api/v1/user":
			writeJSON(t, w, map[string]any{"login": "alice"})
		case "/api/v1/user/orgs":
			writeJSON(t, w, []map[string]any{{"username": "team"}})
		case "/api/v1/user/repos":
			page := r.URL.Query().Get("page")
			if page == "1" {
				writeJSON(t, w, []map[string]any{{"full_name": "alice/app", "clone_url": "https://git.example/alice/app.git"}})
				return
			}
			writeJSON(t, w, []map[string]any{})
		case "/api/v1/orgs/team/repos":
			page := r.URL.Query().Get("page")
			if page == "1" {
				writeJSON(t, w, []map[string]any{{"full_name": "team/lib", "clone_url": "https://git.example/team/lib.git"}})
				return
			}
			writeJSON(t, w, []map[string]any{})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token")
	repos, err := client.ListRepositories(t.Context())
	if err != nil {
		t.Fatalf("ListRepositories returned error: %v", err)
	}
	if !seenAuth {
		t.Fatal("expected Authorization token header")
	}
	if len(repos) != 2 {
		t.Fatalf("repo count = %d, want 2", len(repos))
	}
	if repos[0].FullName != "alice/app" || repos[1].FullName != "team/lib" {
		t.Fatalf("repos = %#v, want user repo followed by org repo", repos)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encoding json: %v", err)
	}
}
