package gitea

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/ghostbladexyz/forge-rescue/internal/rescue"
)

func (c *Client) ExportMetadata(ctx context.Context, repo rescue.Repo, metadataDir string) error {
	owner, name, ok := strings.Cut(repo.FullName, "/")
	if !ok {
		return fmt.Errorf("repo full name must be owner/name: %s", repo.FullName)
	}

	repoDir := filepath.Join(metadataDir, rescue.SafeName(repo.FullName))
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		return err
	}

	endpoints := map[string]string{
		"repo.json": "/api/v1/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name),
	}
	for filename, path := range endpoints {
		if err := c.exportRaw(ctx, path, filepath.Join(repoDir, filename)); err != nil {
			return err
		}
	}
	listEndpoints := map[string]string{
		"issues.json":   "/api/v1/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/issues",
		"releases.json": "/api/v1/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/releases",
		"labels.json":   "/api/v1/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/labels",
	}
	for filename, path := range listEndpoints {
		if err := c.exportPaginatedRaw(ctx, path, filepath.Join(repoDir, filename)); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) exportRaw(ctx context.Context, path string, targetPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "token "+c.token)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("GET %s returned %s", path, resp.Status)
	}

	file, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(file, resp.Body)
	return err
}

func (c *Client) exportPaginatedRaw(ctx context.Context, path string, targetPath string) error {
	var all []json.RawMessage
	for page := 1; ; page++ {
		query := url.Values{}
		query.Set("page", fmt.Sprintf("%d", page))
		query.Set("limit", "50")

		var pageItems []json.RawMessage
		if err := c.get(ctx, path, query, &pageItems); err != nil {
			return err
		}
		if len(pageItems) == 0 {
			break
		}
		all = append(all, pageItems...)
	}

	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(targetPath, data, 0o600)
}
