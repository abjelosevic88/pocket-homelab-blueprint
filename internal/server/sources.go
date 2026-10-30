package server

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-blueprint/internal/store"
)

// ---- where the code comes from ----
//
//   path    a folder on this server under SOURCE_ROOTS, read in place (admins only)
//   git     a repository cloned into DATA_DIR/work/<id>/repo (shallow, updated on rescan)
//   upload  a .zip / .tar.gz unpacked into DATA_DIR/work/<id>/src
//   files   a folder in Pocket Files, fetched as a zip through a Core link on each scan

// prepare makes the code available on disk and returns the folder to scan.
func (s *Server) prepare(ctx context.Context, c *store.Codebase, st store.Settings) (string, error) {
	var root string
	switch c.Source {
	case "path":
		u, err := s.store.GetUser(ctx, c.UserID)
		if err != nil || u.Role != "admin" {
			return "", errors.New("only admins can scan folders on the server")
		}
		p, err := s.allowedPath(c.Location)
		if err != nil {
			return "", err
		}
		root = p
	case "git":
		dir := filepath.Join(s.workDir(c.ID), "repo")
		depth := st.GitCommits
		if depth < 1 {
			depth = 1
		}
		if err := s.gitSync(ctx, c, dir, depth); err != nil {
			return "", err
		}
		root = dir
	case "upload":
		root = filepath.Join(s.workDir(c.ID), "src")
		if _, err := os.Stat(root); err != nil {
			return "", errors.New("the uploaded archive is gone; upload it again")
		}
	case "files":
		if err := s.fetchFromFiles(ctx, c, st); err != nil {
			return "", err
		}
		root = filepath.Join(s.workDir(c.ID), "src")
	default:
		return "", fmt.Errorf("unknown source %q", c.Source)
	}
	if c.Subdir != "" {
		sub, err := cleanSubdir(c.Subdir)
		if err != nil {
			return "", err
		}
		root = filepath.Join(root, filepath.FromSlash(sub))
	}
	fi, err := os.Stat(root)
	if err != nil || !fi.IsDir() {
		return "", fmt.Errorf("the folder %q does not exist", strings.TrimPrefix(c.Subdir, "/"))
	}
	return root, nil
}

// cleanSubdir accepts "a/b" inside the codebase, never "..".
func cleanSubdir(p string) (string, error) {
	p = strings.Trim(strings.ReplaceAll(strings.TrimSpace(p), "\\", "/"), "/")
	if p == "" {
		return "", nil
	}
	c := path.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") || path.IsAbs(c) {
		return "", errors.New("the subfolder must be inside the codebase")
	}
	return c, nil
}

// ---- server folders ----

// roots are SOURCE_ROOTS with symlinks resolved.
func (s *Server) roots() []string {
	var out []string
	for _, r := range s.cfg.SourceRoots {
		if real, err := filepath.EvalSymlinks(r); err == nil {
			out = append(out, real)
		}
	}
	return out
}

// allowedPath resolves a folder and checks it is inside one of SOURCE_ROOTS.
func (s *Server) allowedPath(p string) (string, error) {
	if len(s.cfg.SourceRoots) == 0 {
		return "", errors.New("no server folders are shared with Pocket Blueprint (SOURCE_ROOTS is empty)")
	}
	if !filepath.IsAbs(p) {
		return "", errors.New("give the full path of the folder")
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(p))
	if err != nil {
		return "", fmt.Errorf("the folder %s does not exist", p)
	}
	for _, r := range s.roots() {
		if real == r || strings.HasPrefix(real, r+string(filepath.Separator)) {
			return real, nil
		}
	}
	return "", fmt.Errorf("%s is outside the folders shared with Pocket Blueprint (%s)", p, strings.Join(s.cfg.SourceRoots, ", "))
}

// ---- git ----

var scpLike = regexp.MustCompile(`^[\w.-]+@[\w.-]+:`)

// checkGitURL accepts http(s) repository addresses only (no ssh, file or ext transports).
func checkGitURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if scpLike.MatchString(raw) || strings.HasPrefix(raw, "ssh://") {
		return "", errors.New("use the https address of the repository (ssh needs keys this server does not have)")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", errors.New("give an https address like https://github.com/owner/repo")
	}
	if u.User != nil {
		return "", errors.New("leave the password out of the address; use the token field")
	}
	if strings.HasPrefix(u.Path, "-") || strings.Contains(raw, " ") {
		return "", errors.New("that is not a repository address")
	}
	return raw, nil
}

var credInURL = regexp.MustCompile(`(https?://)[^/@\s]+@`)

// redactURL removes credentials from any URL in a message.
func redactURL(s string) string { return credInURL.ReplaceAllString(s, "$1") }

// git runs git with a locked-down configuration: no prompts, no credential
// helpers, no local/ext transports, no hooks, no system or global config.
func (s *Server) git(ctx context.Context, dir string, c *store.Codebase, args ...string) (string, error) {
	base := []string{"-c", "protocol.file.allow=never", "-c", "protocol.ext.allow=never", "-c", "core.hooksPath=/dev/null",
		"-c", "credential.helper=", "-c", "safe.directory=*", "-c", "advice.detachedHead=false"}
	if c.GitToken != "" {
		user := c.GitUser
		if user == "" {
			user = "git"
		}
		auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + c.GitToken))
		base = append(base, "-c", "http.extraHeader=Authorization: Basic "+auth)
	}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_LFS_SKIP_SMUDGE=1")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	text := strings.TrimSpace(out.String())
	if c.GitToken != "" {
		text = strings.ReplaceAll(text, c.GitToken, "•••")
	}
	if err != nil {
		if ctx.Err() != nil {
			return text, ctx.Err()
		}
		if errors.Is(err, exec.ErrNotFound) {
			return text, errors.New("git is not installed on this server")
		}
		return text, fmt.Errorf("git %s: %s", args[0], lastLines(gitHint(text), 3))
	}
	return text, nil
}

func gitHint(out string) string {
	low := strings.ToLower(out)
	switch {
	case strings.Contains(low, "authentication failed"), strings.Contains(low, "could not read username"), strings.Contains(low, "terminal prompts disabled"):
		return "the repository needs a token (or the token is wrong): add one under Edit"
	case strings.Contains(low, "not found") && strings.Contains(low, "repository"):
		return "the repository was not found (a private one needs a token)"
	case strings.Contains(low, "remote branch") && strings.Contains(low, "not found"):
		return "that branch does not exist"
	case strings.Contains(low, "could not resolve host"):
		return "the server name could not be found"
	}
	return out
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " ")
}

// gitSync clones the repository the first time and later fetches and resets
// to the branch head. The clone is shallow: only the history needed for churn.
func (s *Server) gitSync(ctx context.Context, c *store.Codebase, dir string, depth int) error {
	gctx, cancel := context.WithTimeout(ctx, s.cfg.CloneTimeout)
	defer cancel()
	if _, err := checkGitURL(c.Location); err != nil {
		return err
	}
	d := fmt.Sprint(depth)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		ref := c.Branch
		if ref == "" {
			ref = "HEAD"
		}
		if _, err := s.git(gctx, dir, c, "remote", "set-url", "origin", c.Location); err != nil {
			return err
		}
		if _, err := s.git(gctx, dir, c, "fetch", "--depth", d, "--no-tags", "--prune", "origin", ref); err == nil {
			_, err = s.git(gctx, dir, c, "reset", "--hard", "--quiet", "FETCH_HEAD")
			if err == nil {
				_, _ = s.git(gctx, dir, c, "clean", "-fdxq")
				return nil
			}
		}
		// a broken or re-pointed clone: start over
		_ = os.RemoveAll(dir)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	args := []string{"clone", "--depth", d, "--single-branch", "--no-tags", "--quiet"}
	if c.Branch != "" {
		args = append(args, "--branch", c.Branch)
	}
	args = append(args, "--", c.Location, dir)
	if _, err := s.git(gctx, filepath.Dir(dir), c, args...); err != nil {
		_ = os.RemoveAll(dir)
		return err
	}
	return nil
}

// ---- archives ----

// unpack extracts a .zip or .tar(.gz) into dst (replacing it), skipping
// links and anything that would land outside dst. A single top folder (as in
// GitHub's "Download ZIP") is dropped.
func unpack(archive, dst string, maxBytes int64) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, 4)
	_, _ = io.ReadFull(f, head)
	_, _ = f.Seek(0, io.SeekStart)
	tmp := dst + ".new"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	var written int64
	files := 0
	put := func(name string, mode os.FileMode, r io.Reader) error {
		name = strings.TrimLeft(strings.ReplaceAll(name, "\\", "/"), "/")
		clean := path.Clean(name)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return nil
		}
		if files++; files > 300000 {
			return errors.New("the archive has more than 300,000 files")
		}
		target := filepath.Join(tmp, filepath.FromSlash(clean))
		if mode.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !mode.IsRegular() {
			return nil // links, devices
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		n, err := io.Copy(out, io.LimitReader(r, maxBytes-written+1))
		out.Close()
		if written += n; written > maxBytes {
			return fmt.Errorf("the archive unpacks to more than %d MB", maxBytes>>20)
		}
		return err
	}
	switch {
	case bytes.HasPrefix(head, []byte("PK")):
		fi, _ := f.Stat()
		zr, err := zip.NewReader(f, fi.Size())
		if err != nil {
			return fmt.Errorf("not a readable zip: %w", err)
		}
		for _, zf := range zr.File {
			if err := func() error {
				if zf.FileInfo().IsDir() {
					return put(zf.Name, os.ModeDir, nil)
				}
				rc, err := zf.Open()
				if err != nil {
					return err
				}
				defer rc.Close()
				return put(zf.Name, zf.Mode(), rc)
			}(); err != nil {
				_ = os.RemoveAll(tmp)
				return err
			}
		}
	default:
		var r io.Reader = f
		if head[0] == 0x1f && head[1] == 0x8b {
			gz, err := gzip.NewReader(f)
			if err != nil {
				return err
			}
			defer gz.Close()
			r = gz
		}
		tr := tar.NewReader(r)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				_ = os.RemoveAll(tmp)
				return errors.New("this is not a .zip, .tar or .tar.gz archive")
			}
			mode := os.FileMode(0o644)
			switch h.Typeflag {
			case tar.TypeDir:
				mode = os.ModeDir
			case tar.TypeReg:
			default:
				continue
			}
			if err := put(h.Name, mode, tr); err != nil {
				_ = os.RemoveAll(tmp)
				return err
			}
		}
	}
	// drop a single top folder
	src := tmp
	if ents, err := os.ReadDir(tmp); err == nil && len(ents) == 1 && ents[0].IsDir() {
		src = filepath.Join(tmp, ents[0].Name())
	}
	_ = os.RemoveAll(dst)
	if err := os.Rename(src, dst); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	_ = os.RemoveAll(tmp)
	return nil
}

// ---- Pocket Files ----

// filesLocation splits "space:path" ("home:/Projects/app", "mount:3:/src").
func filesLocation(loc string) (space, p string) {
	i := strings.LastIndex(loc, ":/")
	if i < 0 {
		return "home", "/" + strings.TrimLeft(loc, "/")
	}
	return loc[:i], loc[i+1:]
}

// fetchFromFiles downloads the folder from Pocket Files as a zip and unpacks it.
func (s *Server) fetchFromFiles(ctx context.Context, c *store.Codebase, st store.Settings) error {
	u, err := s.store.GetUser(ctx, c.UserID)
	if err != nil {
		return err
	}
	sib, err := s.siblingFor(ctx, u, st, appFiles, "files:read")
	if err != nil {
		return err
	}
	space, p := filesLocation(c.Location)
	dir := s.workDir(c.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "files-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	limit := s.cfg.MaxUploadMB << 20
	q := url.Values{"space": {space}, "path": {p}}
	if err := sib.client.Download(ctx, "/api/v1/fs/zip", q, tmp, limit); err != nil {
		tmp.Close()
		return fmt.Errorf("Pocket Files: %w", err)
	}
	tmp.Close()
	return unpack(tmp.Name(), filepath.Join(dir, "src"), limit*8)
}
