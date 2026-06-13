package git

import (
	"context"
	"strings"
)

type Remote struct {
	Name     string `json:"name"`
	FetchURL string `json:"fetchUrl"`
	PushURL  string `json:"pushUrl"`
}

// Remotes returns the list of configured remotes for this repo.
// Uses `git remote -v` and merges the (fetch) / (push) pair into a single entry.
// Most repos have identical fetch and push URLs; when different (mirror, fork
// setup, etc.) both are preserved so the frontend can show the right one.
func (r *Repo) Remotes(ctx context.Context, _ struct{}) ([]Remote, error) {
	out, err := run(ctx, r.Path, "remote", "-v")
	if err != nil {
		return nil, err
	}
	byName := map[string]*Remote{}
	order := []string{}
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		// format: "<name>\t<url> (fetch|push)"
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			continue
		}
		name := line[:tab]
		rest := line[tab+1:]
		sp := strings.LastIndexByte(rest, ' ')
		if sp < 0 {
			continue
		}
		url := rest[:sp]
		kind := strings.Trim(rest[sp+1:], "()")
		entry, ok := byName[name]
		if !ok {
			entry = &Remote{Name: name}
			byName[name] = entry
			order = append(order, name)
		}
		switch kind {
		case "fetch":
			entry.FetchURL = url
		case "push":
			entry.PushURL = url
		}
	}
	result := make([]Remote, 0, len(order))
	for _, name := range order {
		result = append(result, *byName[name])
	}
	return result, nil
}
