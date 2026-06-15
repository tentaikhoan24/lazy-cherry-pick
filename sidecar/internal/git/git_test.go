package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo creates a temp git repo with:
//   - main: chore: init
//   - feature/x: feat: add feature x
//
// HEAD is left on main.
func initRepo(t *testing.T) *Repo {
	t.Helper()
	dir := t.TempDir()

	sh := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	sh("git", "init", "-q", "-b", "main")
	sh("git", "config", "user.email", "test@example.com")
	sh("git", "config", "user.name", "Test")
	sh("git", "config", "commit.gpgsign", "false")

	write("README.md", "hello\n")
	sh("git", "add", "README.md")
	sh("git", "commit", "-q", "-m", "chore: init")

	sh("git", "checkout", "-q", "-b", "feature/x")
	write("feature.txt", "feature\n")
	sh("git", "add", "feature.txt")
	sh("git", "commit", "-q", "-m", "feat: add feature x")

	sh("git", "checkout", "-q", "main")

	repo, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return repo
}

// gitSh runs `git <args...>` in dir, failing the test on error.
func gitSh(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_AUTHOR_DATE=2026-01-02T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-02T00:00:00Z")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestOpen_NotARepo(t *testing.T) {
	dir := t.TempDir()
	_, err := Open(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error opening non-repo")
	}
	e, ok := err.(*Error)
	if !ok || e.Code != CodeRepoNotFound {
		t.Errorf("expected CodeRepoNotFound, got %v", err)
	}
}

func TestStatus_Clean(t *testing.T) {
	repo := initRepo(t)
	st, err := repo.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Dirty {
		t.Errorf("expected clean, got dirty")
	}
	if st.Branch != "main" {
		t.Errorf("branch = %q, want main", st.Branch)
	}
	if st.Detached {
		t.Errorf("unexpected detached")
	}
}

func TestStatus_Dirty(t *testing.T) {
	repo := initRepo(t)
	if err := os.WriteFile(filepath.Join(repo.Path, "README.md"), []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo.Path, "new.txt"), []byte("new\n"), 0644); err != nil {
		t.Fatal(err)
	}

	st, err := repo.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.Dirty {
		t.Fatal("expected dirty")
	}
	if len(st.Unstaged) != 1 || st.Unstaged[0].Path != "README.md" {
		t.Errorf("unstaged = %+v", st.Unstaged)
	}
	if len(st.Untracked) != 1 || st.Untracked[0] != "new.txt" {
		t.Errorf("untracked = %+v", st.Untracked)
	}
}

func TestBranches(t *testing.T) {
	repo := initRepo(t)
	bs, err := repo.Branches(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 2 {
		t.Fatalf("want 2 branches, got %d: %+v", len(bs), bs)
	}
	var hasMain, hasFeature bool
	for _, b := range bs {
		switch b.Name {
		case "main":
			hasMain = b.IsHead
		case "feature/x":
			hasFeature = !b.IsHead
		}
	}
	if !hasMain {
		t.Errorf("HEAD main missing")
	}
	if !hasFeature {
		t.Errorf("feature/x missing")
	}
}

func TestCommits(t *testing.T) {
	repo := initRepo(t)
	cs, err := repo.Commits(context.Background(), CommitsArgs{Ref: "feature/x", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 {
		t.Fatalf("want 2 commits, got %d", len(cs))
	}
	if cs[0].Subject != "feat: add feature x" {
		t.Errorf("subject[0] = %q", cs[0].Subject)
	}
	if cs[1].Subject != "chore: init" {
		t.Errorf("subject[1] = %q", cs[1].Subject)
	}
	if len(cs[0].Parents) != 1 {
		t.Errorf("commit[0] should have 1 parent, got %d", len(cs[0].Parents))
	}
	if len(cs[1].Parents) != 0 {
		t.Errorf("root commit should have 0 parents, got %d", len(cs[1].Parents))
	}
}

func TestCommits_FilterMessage(t *testing.T) {
	repo := initRepo(t)
	cs, err := repo.Commits(context.Background(), CommitsArgs{
		Ref:    "feature/x",
		Limit:  10,
		Filter: CommitFilter{MessageContains: "feat:"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 {
		t.Fatalf("want 1, got %d", len(cs))
	}
	if !strings.Contains(cs[0].Subject, "feat:") {
		t.Errorf("unexpected subject: %q", cs[0].Subject)
	}
}

func TestCherryPick_Happy(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()

	cs, _ := repo.Commits(ctx, CommitsArgs{Ref: "feature/x", Limit: 1})
	sha := cs[0].Sha

	r, err := repo.CherryPick(ctx, CherryPickArgs{Target: "main", Shas: []string{sha}})
	if err != nil {
		t.Fatalf("cherry-pick: %v", err)
	}
	if len(r.Applied) != 1 {
		t.Errorf("want 1 applied, got %d", len(r.Applied))
	}
	if len(r.Conflicts) != 0 {
		t.Errorf("want no conflicts, got %+v", r.Conflicts)
	}

	// Verify the commit landed on main
	mainCommits, _ := repo.Commits(ctx, CommitsArgs{Ref: "main", Limit: 5})
	if mainCommits[0].Subject != "feat: add feature x" {
		t.Errorf("main HEAD subject = %q", mainCommits[0].Subject)
	}
}

func TestCherryPick_DirtyTreeBlocked(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()

	os.WriteFile(filepath.Join(repo.Path, "README.md"), []byte("dirty\n"), 0644)

	cs, _ := repo.Commits(ctx, CommitsArgs{Ref: "feature/x", Limit: 1})
	_, err := repo.CherryPick(ctx, CherryPickArgs{Target: "main", Shas: []string{cs[0].Sha}})
	if err == nil {
		t.Fatal("expected dirty-tree error")
	}
	e, ok := err.(*Error)
	if !ok || e.Code != CodeDirtyTree {
		t.Errorf("expected CodeDirtyTree, got %v (%T)", err, err)
	}
}

func TestCherryPick_Conflict(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()

	// Make main's README diverge from feature/x's
	os.WriteFile(filepath.Join(repo.Path, "README.md"), []byte("main version\n"), 0644)
	exec.Command("git", "-C", repo.Path, "commit", "-am", "docs: main version").Run()

	// Make feature/x's README diverge differently
	exec.Command("git", "-C", repo.Path, "checkout", "feature/x").Run()
	os.WriteFile(filepath.Join(repo.Path, "README.md"), []byte("feature version\n"), 0644)
	exec.Command("git", "-C", repo.Path, "commit", "-am", "docs: feature version").Run()
	out, _ := exec.Command("git", "-C", repo.Path, "rev-parse", "HEAD").Output()
	conflictSha := strings.TrimSpace(string(out))
	exec.Command("git", "-C", repo.Path, "checkout", "main").Run()

	// Try to cherry-pick the feature commit → expect conflict
	r, err := repo.CherryPick(ctx, CherryPickArgs{Target: "main", Shas: []string{conflictSha}})
	if err == nil {
		t.Fatal("expected conflict error")
	}
	e, ok := err.(*Error)
	if !ok || e.Code != CodeCherryPickConflict {
		t.Fatalf("expected CodeCherryPickConflict, got %v", err)
	}
	if len(r.Conflicts) != 1 || r.Conflicts[0].Files[0] != "README.md" {
		t.Errorf("conflicts = %+v", r.Conflicts)
	}

	// Repo should be left in conflict state (M5 leave-in-conflict behavior)
	st, _ := repo.Status(ctx)
	if !st.Dirty {
		t.Errorf("repo should be in conflict state after cherry-pick conflict, got: %+v", st)
	}

	// After explicit abort, repo should be clean again
	if err := repo.Abort(ctx); err != nil {
		t.Fatalf("abort failed: %v", err)
	}
	st2, _ := repo.Status(ctx)
	if st2.Dirty {
		t.Errorf("repo should be clean after abort, got: %+v", st2)
	}
}

// TestPull_CheckedOutBranch covers the M12b bug: "git fetch origin main:main"
// is refused by git when main is the checked-out branch. Pull must fall back
// to fetch + `merge --ff-only` so the working tree picks up the new commit.
func TestPull_CheckedOutBranch(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()

	// A second repo, cloned from repo, acts as "origin" with a new commit on main.
	remoteDir := t.TempDir()
	if out, err := exec.Command("git", "clone", "-q", repo.Path, remoteDir).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	gitSh(t, remoteDir, "config", "user.email", "test@example.com")
	gitSh(t, remoteDir, "config", "user.name", "Test")
	gitSh(t, remoteDir, "config", "commit.gpgsign", "false")
	gitSh(t, remoteDir, "checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(remoteDir, "remote.txt"), []byte("from remote\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitSh(t, remoteDir, "add", "remote.txt")
	gitSh(t, remoteDir, "commit", "-q", "-m", "feat: remote update")

	gitSh(t, repo.Path, "remote", "add", "origin", remoteDir)
	if _, err := run(ctx, repo.Path, "fetch", "origin"); err != nil {
		t.Fatalf("fetch: %v", err)
	}

	// main is checked out in repo — must take the fetch+merge --ff-only path.
	res, err := repo.Pull(ctx, PullArgs{Branch: "main", Remote: "origin"})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if res.Branch != "main" || res.Remote != "origin" {
		t.Errorf("unexpected result: %+v", res)
	}

	if _, err := os.Stat(filepath.Join(repo.Path, "remote.txt")); err != nil {
		t.Errorf("expected remote.txt in working tree after pull: %v", err)
	}
	st, _ := repo.Status(ctx)
	if st.Dirty {
		t.Errorf("expected clean tree after ff-only merge, got: %+v", st)
	}
}

// TestPull_NonCheckedOutBranch covers the original M4 use case: fast-forwarding
// a local branch that is NOT checked out, via "git fetch origin branch:branch".
func TestPull_NonCheckedOutBranch(t *testing.T) {
	repo := initRepo(t) // HEAD = main; feature/x exists but is not checked out

	remoteDir := t.TempDir()
	if out, err := exec.Command("git", "clone", "-q", repo.Path, remoteDir).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	gitSh(t, remoteDir, "config", "user.email", "test@example.com")
	gitSh(t, remoteDir, "config", "user.name", "Test")
	gitSh(t, remoteDir, "config", "commit.gpgsign", "false")
	gitSh(t, remoteDir, "checkout", "-q", "feature/x")
	if err := os.WriteFile(filepath.Join(remoteDir, "feature2.txt"), []byte("more feature\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitSh(t, remoteDir, "add", "feature2.txt")
	gitSh(t, remoteDir, "commit", "-q", "-m", "feat: more feature")

	ctx := context.Background()
	gitSh(t, repo.Path, "remote", "add", "origin", remoteDir)
	if _, err := run(ctx, repo.Path, "fetch", "origin"); err != nil {
		t.Fatalf("fetch: %v", err)
	}

	res, err := repo.Pull(ctx, PullArgs{Branch: "feature/x", Remote: "origin"})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if res.Branch != "feature/x" {
		t.Errorf("unexpected result: %+v", res)
	}

	cs, err := repo.Commits(ctx, CommitsArgs{Ref: "feature/x", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if cs[0].Subject != "feat: more feature" {
		t.Errorf("feature/x HEAD = %q, want updated commit", cs[0].Subject)
	}
}
