package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func NewClient(token string) *Client {
	return NewClientWithBaseURL("https://api.github.com", token)
}

func NewClientWithBaseURL(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) RepositoryExists(ctx context.Context, owner, name string) (bool, error) {
	resp, err := c.do(ctx, http.MethodGet, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name), nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return false, fmt.Errorf("GET repository returned %s", resp.Status)
	}
	return true, nil
}

func (c *Client) CreateRepository(ctx context.Context, owner, name string, private bool) error {
	body := map[string]any{
		"name":       name,
		"private":    private,
		"auto_init":  false,
		"has_issues": true,
	}
	resp, err := c.do(ctx, http.MethodPost, "/user/repos", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("create GitHub repository %s/%s returned %s", owner, name, resp.Status)
	}
	return nil
}

func (c *Client) HasRefs(ctx context.Context, owner, name string) (bool, error) {
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/git/matching-refs"
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		return false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return false, fmt.Errorf("GET repository refs returned %s", resp.Status)
	}

	var refs []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&refs); err != nil {
		return false, err
	}
	return len(refs) > 0, nil
}

func (c *Client) DeleteRepository(ctx context.Context, owner, name string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("delete GitHub repository %s/%s returned %s", owner, name, resp.Status)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, value any) (*http.Response, error) {
	var body *bytes.Reader
	if value == nil {
		body = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if value != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.httpClient.Do(req)
}
