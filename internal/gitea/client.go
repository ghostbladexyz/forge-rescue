package gitea

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ghostbladexyz/forge-rescue/internal/rescue"
)

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

type User struct {
	Login string `json:"login"`
}

type Organization struct {
	UserName string `json:"username"`
	Name     string `json:"name"`
}

func NewClient(instanceURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(instanceURL, "/"),
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) CurrentUser(ctx context.Context) (User, error) {
	var user User
	err := c.get(ctx, "/api/v1/user", nil, &user)
	return user, err
}

func (c *Client) ListRepositories(ctx context.Context) ([]rescue.Repo, error) {
	var repos []rescue.Repo
	userRepos, err := c.listRepoPath(ctx, "/api/v1/user/repos")
	if err != nil {
		return nil, err
	}
	repos = append(repos, userRepos...)

	var orgs []Organization
	if err := c.get(ctx, "/api/v1/user/orgs", nil, &orgs); err != nil {
		return nil, err
	}
	for _, org := range orgs {
		name := org.UserName
		if name == "" {
			name = org.Name
		}
		if name == "" {
			continue
		}
		orgRepos, err := c.listRepoPath(ctx, "/api/v1/orgs/"+url.PathEscape(name)+"/repos")
		if err != nil {
			return nil, err
		}
		repos = append(repos, orgRepos...)
	}
	return repos, nil
}

func (c *Client) listRepoPath(ctx context.Context, path string) ([]rescue.Repo, error) {
	var all []rescue.Repo
	for page := 1; ; page++ {
		query := url.Values{}
		query.Set("page", fmt.Sprintf("%d", page))
		query.Set("limit", "50")

		var repos []rescue.Repo
		if err := c.get(ctx, path, query, &repos); err != nil {
			return nil, err
		}
		if len(repos) == 0 {
			break
		}
		all = append(all, repos...)
	}
	return all, nil
}

func (c *Client) get(ctx context.Context, path string, query url.Values, target any) error {
	endpoint := c.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
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
	return json.NewDecoder(resp.Body).Decode(target)
}
