package analyze

import (
	"bufio"
	"bytes"
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// gitRun runs one read-only git command in the root. Config that could run
// programs (fsmonitor, hooks, external diff, textconv, pager) is disabled.
func (a *analyzer) gitRun(ctx context.Context, args ...string) ([]byte, error) {
	base := []string{"-c", "safe.directory=*", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null",
		"-c", "core.quotepath=off", "-c", "diff.external=", "-c", "log.showSignature=false", "--no-pager", "-C", a.root}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "PAGER=cat", "GIT_OPTIONAL_LOCKS=0",
		"LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_EXTERNAL_DIFF=")
	cmd.Stdin = nil
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	return cmd.Output()
}

// hasGitDir looks for .git in the root or one of its parents (stat only).
func hasGitDir(root string) bool {
	for d := root; ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return true
		}
		p := filepath.Dir(d)
		if p == d {
			return false
		}
		d = p
	}
}

// git reads branch, head and per-file churn from the history.
func (a *analyzer) git() {
	if !hasGitDir(a.root) {
		return
	}
	if _, err := exec.LookPath("git"); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(a.ctx, 60*time.Second)
	defer cancel()
	out, err := a.gitRun(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return // not a repository after all, or no commits yet
	}
	gi := &GitInfo{Branch: strings.TrimSpace(string(out)), Authors: []Author{}}
	if out, err := a.gitRun(ctx, "log", "-1", "--format=%h%x1f%cI%x1f%s"); err == nil {
		parts := strings.SplitN(strings.TrimSpace(string(out)), "\x1f", 3)
		if len(parts) == 3 {
			gi.Head, gi.HeadDate, gi.HeadMsg = parts[0], parts[1], clip(parts[2], 200)
		}
	}
	if out, err := a.gitRun(ctx, "remote", "get-url", "origin"); err == nil {
		gi.Remote = stripCredentials(strings.TrimSpace(string(out)))
	}
	out, err = a.gitRun(ctx, "log", "-n", strconv.Itoa(a.opt.GitMaxCommits), "--no-merges", "--numstat",
		"--no-ext-diff", "--no-textconv", "--relative", "--format=%x1e%an%x1f%aI", "--", ".")
	if err != nil {
		if ctx.Err() != nil {
			a.warnf("Reading the git history took too long and was stopped")
		} else {
			a.warnf("The git history could not be read")
		}
		a.res.Git = gi
		return
	}
	type stat struct {
		churn, added, deleted int
		authors               map[string]bool
		last                  string
	}
	stats := map[string]*stat{}
	alias := map[string]string{} // old path → newest path
	resolve := func(p string) string {
		for i := 0; i < 20; i++ {
			n, ok := alias[p]
			if !ok {
				break
			}
			p = n
		}
		return p
	}
	authors := map[string]int{}
	var author, date string
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "\x1e") {
			parts := strings.SplitN(line[1:], "\x1f", 2)
			author, date = parts[0], ""
			if len(parts) == 2 {
				date = parts[1]
			}
			gi.Commits++
			authors[author]++
			if gi.Commits == 1 && gi.HeadDate == "" {
				gi.HeadDate = date
			}
			gi.FirstDate = date
			continue
		}
		fs := strings.SplitN(line, "\t", 3)
		if len(fs) != 3 {
			continue
		}
		p := fs[2]
		if oldP, newP, ok := renamePaths(p); ok {
			np := resolve(newP)
			if oldP != np {
				alias[oldP] = np
			}
			p = np
		} else {
			p = resolve(p)
		}
		s := stats[p]
		if s == nil {
			s = &stat{authors: map[string]bool{}, last: date}
			stats[p] = s
		}
		s.churn++
		s.added += atoiDash(fs[0])
		s.deleted += atoiDash(fs[1])
		s.authors[author] = true
	}
	for i := range a.res.Files {
		f := &a.res.Files[i]
		if s, ok := stats[f.Path]; ok {
			f.Churn, f.Added, f.Deleted, f.Authors, f.LastChange = s.churn, s.added, s.deleted, len(s.authors), s.last
		}
	}
	for n, c := range authors {
		gi.Authors = append(gi.Authors, Author{Name: n, Commits: c})
	}
	sort.Slice(gi.Authors, func(i, j int) bool {
		if gi.Authors[i].Commits != gi.Authors[j].Commits {
			return gi.Authors[i].Commits > gi.Authors[j].Commits
		}
		return gi.Authors[i].Name < gi.Authors[j].Name
	})
	if len(gi.Authors) > 30 {
		gi.Authors = gi.Authors[:30]
	}
	a.res.Git = gi
}

func atoiDash(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// renamePaths reads numstat rename syntax: "a => b" or "dir/{a => b}/f".
func renamePaths(p string) (string, string, bool) {
	if !strings.Contains(p, " => ") {
		return "", "", false
	}
	if i := strings.IndexByte(p, '{'); i >= 0 {
		j := strings.IndexByte(p[i:], '}')
		if j < 0 {
			return "", "", false
		}
		inner := p[i+1 : i+j]
		parts := strings.SplitN(inner, " => ", 2)
		if len(parts) != 2 {
			return "", "", false
		}
		pre, post := p[:i], p[i+j+1:]
		clean := func(s string) string { return strings.ReplaceAll(s, "//", "/") }
		return clean(pre + parts[0] + post), clean(pre + parts[1] + post), true
	}
	parts := strings.SplitN(p, " => ", 2)
	return parts[0], parts[1], true
}

// stripCredentials removes user:password@ from a remote URL.
func stripCredentials(s string) string {
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err == nil {
			u.User = nil
			return u.String()
		}
		// Unparseable: drop everything between :// and @.
		if i, j := strings.Index(s, "://"), strings.LastIndex(s, "@"); j > i {
			return s[:i+3] + s[j+1:]
		}
	}
	return s
}
