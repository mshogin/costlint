package forecast

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Commit holds information about a single git commit.
type Commit struct {
	Hash          string
	Subject       string
	FilesChanged  int
	Insertions    int
	Deletions     int
}

// BranchStats contains aggregate diff statistics for a branch range.
type BranchStats struct {
	FilesChanged int
	Insertions   int
	Deletions    int
}

// currentBranch returns the name of the current git branch.
func currentBranch() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git not found or not in a git repository: %w", err)
	}
	branch := strings.TrimSpace(string(out))
	if branch == "HEAD" {
		return "", fmt.Errorf("detached HEAD state; use --branch flag to specify the branch")
	}
	return branch, nil
}

// gitLog returns the commits in branch that are not in base (base..branch).
func gitLog(base, branch string) ([]Commit, error) {
	// Get commit hashes and subjects
	logOut, err := exec.Command("git", "log", "--oneline", base+".."+branch).Output()
	if err != nil {
		return nil, fmt.Errorf("git log failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(logOut)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}

	var commits []Commit
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		c := Commit{Hash: parts[0]}
		if len(parts) > 1 {
			c.Subject = parts[1]
		}
		commits = append(commits, c)
	}

	// Enrich each commit with diff stats
	for i, c := range commits {
		stats, err := commitDiffStat(c.Hash)
		if err == nil {
			commits[i].FilesChanged = stats.FilesChanged
			commits[i].Insertions = stats.Insertions
			commits[i].Deletions = stats.Deletions
		}
	}

	return commits, nil
}

// commitDiffStat returns diff stats for a single commit.
func commitDiffStat(hash string) (BranchStats, error) {
	out, err := exec.Command("git", "diff", "--shortstat", hash+"^", hash).Output()
	if err != nil {
		// First commit has no parent - try against empty tree
		out, err = exec.Command("git", "diff", "--shortstat", "4b825dc642cb6eb9a060e54bf8d69288fbee4904", hash).Output()
		if err != nil {
			return BranchStats{}, err
		}
	}
	return parseShortstat(string(out))
}

// gitDiffStat returns aggregate diff statistics for base..branch.
func gitDiffStat(base, branch string) (BranchStats, error) {
	out, err := exec.Command("git", "diff", "--shortstat", base+"..."+branch).Output()
	if err != nil {
		return BranchStats{}, fmt.Errorf("git diff failed: %w", err)
	}
	return parseShortstat(string(out))
}

// parseShortstat parses the output of `git diff --shortstat`.
// Example: " 3 files changed, 45 insertions(+), 12 deletions(-)"
func parseShortstat(s string) (BranchStats, error) {
	var stats BranchStats
	s = strings.TrimSpace(s)
	if s == "" {
		return stats, nil
	}

	parts := strings.Split(s, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		fields := strings.Fields(part)
		if len(fields) < 2 {
			continue
		}
		n, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		switch {
		case strings.Contains(part, "file"):
			stats.FilesChanged = n
		case strings.Contains(part, "insertion"):
			stats.Insertions = n
		case strings.Contains(part, "deletion"):
			stats.Deletions = n
		}
	}
	return stats, nil
}
