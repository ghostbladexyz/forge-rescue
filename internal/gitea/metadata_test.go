package gitea

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ghostbladexyz/forge-rescue/internal/rescue"
)

// TestCaptureMetadataReturnsCompleteRawArchive verifies all required records arrive before the capture is returned.
func TestCaptureMetadataReturnsCompleteRawArchive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/team/lib":
			writeJSON(t, w, map[string]any{"id": 9, "full_name": "team/lib", "custom_field": "preserved"})
		case "/api/v1/repos/team/lib/issues":
			if r.URL.Query().Get("page") == "1" {
				writeNextPage(w, r)
				writeJSON(t, w, []map[string]any{{"id": 1, "title": "keep me"}})
				return
			}
			writeLastPage(w)
			writeJSON(t, w, []map[string]any{{"id": 1, "title": "overlap"}, {"id": 2, "title": "keep me too"}})
		case "/api/v1/repos/team/lib/releases":
			writeLastPage(w)
			writeJSON(t, w, []map[string]any{{"id": 3, "tag_name": "v1.0.0"}})
		case "/api/v1/repos/team/lib/labels":
			writeLastPage(w)
			writeJSON(t, w, []map[string]any{{"id": 4, "name": "bug"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	source, err := NewSource(server.URL, "token")
	if err != nil {
		t.Fatalf("NewSource returned error: %v", err)
	}
	metadata, err := source.CaptureMetadata(context.Background(), rescue.Repo{FullName: "team/lib"})
	if err != nil {
		t.Fatalf("CaptureMetadata returned error: %v", err)
	}
	if !strings.Contains(string(metadata.Repository), `"custom_field":"preserved"`) {
		t.Fatalf("repository metadata = %s, want provider field", metadata.Repository)
	}
	if len(metadata.Issues) != 2 || !strings.Contains(string(metadata.Issues[1]), "keep me too") {
		t.Fatalf("issues = %s, want two deduplicated records", metadata.Issues)
	}
	if len(metadata.Releases) != 1 || len(metadata.Labels) != 1 {
		t.Fatalf("release/label counts = %d/%d, want 1/1", len(metadata.Releases), len(metadata.Labels))
	}
}

// TestCaptureMetadataRejectsInvalidRepositoryNamesBeforeRequest verifies endpoint paths always represent one owner and repository.
func TestCaptureMetadataRejectsInvalidRepositoryNamesBeforeRequest(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.NotFound(w, r)
	}))
	defer server.Close()

	source, err := NewSource(server.URL, "token")
	if err != nil {
		t.Fatalf("NewSource returned error: %v", err)
	}
	for _, fullName := range []string{"owner", "/repo", "owner/", "owner/repo/extra"} {
		if _, err := source.CaptureMetadata(context.Background(), rescue.Repo{FullName: fullName}); err == nil {
			t.Errorf("CaptureMetadata(%q) returned nil error", fullName)
		}
	}
	if requests != 0 {
		t.Fatalf("request count = %d, want validation before requests", requests)
	}
}

// TestCaptureMetadataReturnsNoPartialValue verifies one failed collection invalidates the entire in-memory capture.
func TestCaptureMetadataReturnsNoPartialValue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/issues") {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(t, w, map[string]any{"id": 9})
	}))
	defer server.Close()

	source, err := NewSource(server.URL, "token")
	if err != nil {
		t.Fatalf("NewSource returned error: %v", err)
	}
	metadata, err := source.CaptureMetadata(context.Background(), rescue.Repo{FullName: "team/lib"})
	if err == nil || !strings.Contains(err.Error(), "capture issues for team/lib") {
		t.Fatalf("CaptureMetadata error = %v, want issue context", err)
	}
	if metadata.Repository != nil || metadata.Issues != nil || metadata.Releases != nil || metadata.Labels != nil {
		t.Fatalf("metadata = %#v, want zero value after partial failure", metadata)
	}
}
