package upload

import (
	"context"
	"os"
)

type createdRepo struct {
	owner   string
	name    string
	private bool
}

type recordingGitHub struct {
	repos   map[string]bool
	refs    map[string]bool
	created []createdRepo
}

func (g *recordingGitHub) RepositoryExists(ctx context.Context, owner, name string) (bool, error) {
	return g.repos[owner+"/"+name], nil
}

func (g *recordingGitHub) CreateRepository(ctx context.Context, owner, name string, private bool) error {
	g.created = append(g.created, createdRepo{owner: owner, name: name, private: private})
	if g.repos == nil {
		g.repos = map[string]bool{}
	}
	g.repos[owner+"/"+name] = true
	return nil
}

func (g *recordingGitHub) HasRefs(ctx context.Context, owner, name string) (bool, error) {
	return g.refs[owner+"/"+name], nil
}

type recordingRunner struct {
	calls []commandCall
}

type commandCall struct {
	name string
	args []string
}

func (r *recordingRunner) Run(ctx context.Context, name string, args ...string) error {
	r.calls = append(r.calls, commandCall{name: name, args: args})
	return nil
}

func mkdir(path string) error {
	return os.MkdirAll(path, 0o755)
}
