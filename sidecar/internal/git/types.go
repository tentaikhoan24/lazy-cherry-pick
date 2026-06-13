package git

// Status mirrors the result of `git.status`. See docs/IPC.md.
type Status struct {
	Branch    string       `json:"branch"`
	Detached  bool         `json:"detached"`
	Upstream  string       `json:"upstream,omitempty"`
	Ahead     int          `json:"ahead"`
	Behind    int          `json:"behind"`
	Dirty     bool         `json:"dirty"`
	Staged    []FileStatus `json:"staged"`
	Unstaged  []FileStatus `json:"unstaged"`
	Untracked []string     `json:"untracked"`
}

// FileStatus is one porcelain v2 entry. XY is the two-char code
// (e.g. "M.", ".M", "AA", "UU"); '.' means unchanged on that side.
type FileStatus struct {
	Path    string `json:"path"`
	OldPath string `json:"oldPath,omitempty"`
	XY      string `json:"xy"`
}

type Branch struct {
	Name     string `json:"name"`
	Sha      string `json:"sha"`
	IsHead   bool   `json:"isHead"`
	Upstream string `json:"upstream,omitempty"`
	Remote   bool   `json:"remote"`
}

type Commit struct {
	Sha     string   `json:"sha"`
	Parents []string `json:"parents"`
	Author  string   `json:"author"`
	Email   string   `json:"email"`
	Time    int64    `json:"time"`
	Subject string   `json:"subject"`
	Refs    []string `json:"refs,omitempty"`
}

// CommitFilter is the M2 subset of the filter spec in docs/IPC.md.
// Advanced fields (regex, JIRA, labels) are deferred to M2.5+.
type CommitFilter struct {
	Author          string `json:"author,omitempty"`
	MessageContains string `json:"messageContains,omitempty"`
	PathGlob        string `json:"pathGlob,omitempty"`
	Since           string `json:"since,omitempty"`
	Until           string `json:"until,omitempty"`
}

type CherryPickResult struct {
	Applied   []string       `json:"applied"`
	Skipped   []string       `json:"skipped"`
	Conflicts []ConflictInfo `json:"conflicts"`
}

type ConflictInfo struct {
	Sha   string   `json:"sha"`
	Files []string `json:"files"`
}
