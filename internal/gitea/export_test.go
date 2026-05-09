package gitea

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghostbladexyz/forge-rescue/internal/rescue"
)

func TestExportMetadataWritesRawEndpointJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/team/lib":
			writeJSON(t, w, map[string]any{"full_name": "team/lib"})
		case "/api/v1/repos/team/lib/issues":
			if r.URL.Query().Get("page") == "1" {
				writeJSON(t, w, []map[string]any{{"number": 1, "title": "keep me"}})
				return
			}
			if r.URL.Query().Get("page") == "2" {
				writeJSON(t, w, []map[string]any{{"number": 2, "title": "keep me too"}})
				return
			}
			writeJSON(t, w, []map[string]any{})
		case "/api/v1/repos/team/lib/releases":
			if r.URL.Query().Get("page") != "1" {
				writeJSON(t, w, []map[string]any{})
				return
			}
			writeJSON(t, w, []map[string]any{{"tag_name": "v1.0.0"}})
		case "/api/v1/repos/team/lib/labels":
			if r.URL.Query().Get("page") != "1" {
				writeJSON(t, w, []map[string]any{})
				return
			}
			writeJSON(t, w, []map[string]any{{"name": "bug"}})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	tmp := t.TempDir()
	client := NewClient(server.URL, "token")
	if err := client.ExportMetadata(t.Context(), rescue.Repo{FullName: "team/lib"}, tmp); err != nil {
		t.Fatalf("ExportMetadata returned error: %v", err)
	}

	assertContains(t, filepath.Join(tmp, "team-lib", "issues.json"), `"title": "keep me"`)
	assertContains(t, filepath.Join(tmp, "team-lib", "issues.json"), `"title": "keep me too"`)
	assertContains(t, filepath.Join(tmp, "team-lib", "releases.json"), `"tag_name": "v1.0.0"`)
	assertContains(t, filepath.Join(tmp, "team-lib", "labels.json"), `"name": "bug"`)
}

func assertContains(t *testing.T, path string, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if !strings.Contains(string(data), want) {
		t.Fatalf("%s = %s, want substring %s", path, string(data), want)
	}
}
