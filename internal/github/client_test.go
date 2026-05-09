package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateRepositoryCreatesPrivateUserRepo(t *testing.T) {
	var gotPath string
	var gotPrivate bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("Authorization = %q, want Bearer token", r.Header.Get("Authorization"))
		}
		var body struct {
			Name    string `json:"name"`
			Private bool   `json:"private"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		gotPrivate = body.Private
		if body.Name != "alice-project" {
			t.Fatalf("name = %q, want alice-project", body.Name)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.URL, "token")
	if err := client.CreateRepository(context.Background(), "ghostbladexyz", "alice-project", true); err != nil {
		t.Fatalf("CreateRepository returned error: %v", err)
	}
	if gotPath != "/user/repos" {
		t.Fatalf("path = %q, want /user/repos", gotPath)
	}
	if !gotPrivate {
		t.Fatalf("private = false, want true")
	}
}

func TestRepositoryExistsReturnsFalseOnNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.URL, "token")
	exists, err := client.RepositoryExists(context.Background(), "ghostbladexyz", "alice-project")
	if err != nil {
		t.Fatalf("RepositoryExists returned error: %v", err)
	}
	if exists {
		t.Fatalf("exists = true, want false")
	}
}

func TestHasRefsTreatsEmptyRepositoryAsNoRefs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.URL, "token")
	hasRefs, err := client.HasRefs(context.Background(), "ghostbladexyz", "alice-project")
	if err != nil {
		t.Fatalf("HasRefs returned error: %v", err)
	}
	if hasRefs {
		t.Fatalf("hasRefs = true, want false")
	}
}

func TestDeleteRepositoryDeletesOwnerRepo(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != http.MethodDelete {
			t.Fatalf("method = %q, want DELETE", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("Authorization = %q, want Bearer token", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.URL, "token")
	if err := client.DeleteRepository(context.Background(), "ghostbladexyz", "alice-project"); err != nil {
		t.Fatalf("DeleteRepository returned error: %v", err)
	}
	if gotPath != "/repos/ghostbladexyz/alice-project" {
		t.Fatalf("path = %q, want /repos/ghostbladexyz/alice-project", gotPath)
	}
}
