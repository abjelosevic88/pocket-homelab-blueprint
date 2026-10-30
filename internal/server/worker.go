package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-blueprint/internal/analyze"
	"github.com/abjelosevic88/pocket-homelab-blueprint/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-blueprint/internal/store"
)

// ---- the scan queue ----
//
// Scans are queued rows in the database; one worker takes them oldest first,
// one at a time (a scan reads a lot of files, two at once only make both
// slower). A queued or running scan can be cancelled. A restart marks the
// scan that was running as failed and carries on with the queue.

// nudge wakes the worker after a scan was queued.
func (s *Server) nudge() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Server) worker(ctx context.Context) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		ids, err := s.store.QueuedScans(ctx)
		if err != nil && ctx.Err() == nil {
			s.log.Warn("scan queue", "err", err)
		}
		for _, id := range ids {
			if ctx.Err() != nil {
				return
			}
			s.runScan(ctx, id)
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-tick.C:
		}
	}
}

// workDir is where a codebase's clone or unpacked archive lives.
func (s *Server) workDir(cid int64) string {
	return filepath.Join(s.cfg.DataDir, "work", fmt.Sprint(cid))
}

// stages map the analyzer's stages onto one 0–100 bar.
var stages = map[string][2]int{"prepare": {0, 12}, "files": {12, 30}, "parsing": {30, 78}, "linking": {78, 90}, "history": {90, 99}}

var stageNames = map[string]string{"prepare": "Getting the code", "files": "Listing files", "parsing": "Reading the code", "linking": "Connecting files", "history": "Reading the history"}

func (s *Server) runScan(parent context.Context, id int64) {
	ok, err := s.store.StartScan(parent, id)
	if err != nil || !ok {
		return
	}
	sc, err := s.store.ScanByID(parent, id)
	if err != nil {
		return
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(parent, s.cfg.ScanTimeout+s.cfg.CloneTimeout)
	defer cancel()
	s.runMu.Lock()
	s.running[id] = cancel
	s.runMu.Unlock()
	defer func() {
		s.runMu.Lock()
		delete(s.running, id)
		s.runMu.Unlock()
	}()

	fail := func(err error) {
		status, msg := "failed", plainScanErr(err)
		if s.store.ScanStatus(context.Background(), id) == "cancelled" {
			return
		}
		if errors.Is(err, context.Canceled) && parent.Err() == nil {
			status, msg = "cancelled", ""
		}
		_ = s.store.FailScan(context.Background(), id, status, msg, time.Since(started).Milliseconds())
		if status == "failed" {
			s.log.Warn("scan failed", "scan", id, "codebase", sc.CodebaseID, "err", err)
			s.afterScan(context.Background(), sc, nil, msg)
		}
	}
	c, err := s.store.CodebaseByID(ctx, sc.CodebaseID)
	if err != nil {
		fail(err)
		return
	}
	st, _ := s.store.GetSettings(ctx, c.UserID)

	var mu sync.Mutex
	last := time.Time{}
	report := func(stage string, done, total int) {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(last) < time.Second && done < total {
			return
		}
		last = time.Now()
		r, ok := stages[stage]
		if !ok {
			return
		}
		p := r[0]
		if total > 0 {
			p += (r[1] - r[0]) * min(done, total) / total
		}
		name := stageNames[stage]
		if total > 0 && stage != "prepare" {
			name = fmt.Sprintf("%s (%d of %d)", name, min(done, total), total)
		}
		_ = s.store.ScanProgress(context.Background(), id, name, p)
	}
	report("prepare", 0, 1)
	root, err := s.prepare(ctx, c, st)
	if err != nil {
		fail(err)
		return
	}
	actx, acancel := context.WithTimeout(ctx, s.cfg.ScanTimeout)
	defer acancel()
	commits := st.GitCommits
	if commits <= 0 {
		commits = -1
	}
	res, err := analyze.Run(actx, root, analyze.Options{MaxFiles: s.cfg.MaxFiles, GitMaxCommits: commits, Vendored: c.Vendored}, report)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("the scan took longer than %s; scan a smaller folder (Subfolder) or raise SCAN_TIMEOUT", s.cfg.ScanTimeout)
		}
		fail(err)
		return
	}
	res.Name = c.Name // the folder is "src" or "repo" here; the codebase's name is what people know
	blob, err := gzipJSON(res)
	if err != nil {
		fail(err)
		return
	}
	stats, _ := json.Marshal(res.Stats)
	sc.Stats = stats
	if res.Git != nil {
		sc.Commit, sc.Branch = res.Git.Head, res.Git.Branch
	}
	sc.FinishedAt = store.Now()
	sc.DurationMS = time.Since(started).Milliseconds()
	if err := s.store.FinishScan(context.Background(), sc, blob); err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			fail(err)
		}
		return
	}
	s.scanned.Add(1)
	s.results.put(sc.ID, res)
	_ = s.store.PruneScans(context.Background(), c.ID, st.KeepScans)
	s.log.Info("scan finished", "scan", id, "codebase", c.ID, "files", res.Stats.Files, "took", time.Since(started).Round(time.Millisecond).String())
	s.afterScan(context.Background(), sc, res, "")
}

// cancelScan stops a running scan right away (a queued one is just marked).
func (s *Server) cancelScan(id int64) {
	s.runMu.Lock()
	if c, ok := s.running[id]; ok {
		c()
	}
	s.runMu.Unlock()
}

// afterScan publishes the event and sends the person's notification.
func (s *Server) afterScan(ctx context.Context, sc *store.Scan, res *analyze.Result, failure string) {
	c, err := s.store.CodebaseByID(ctx, sc.CodebaseID)
	if err != nil {
		return
	}
	st, _ := s.store.GetSettings(ctx, c.UserID)
	link := ""
	if s.cfg.BaseURL != "" {
		link = fmt.Sprintf("%s/c/%d", s.cfg.BaseURL, c.ID)
	}
	n := notify.New(st.NotifyURL, st.NotifyFormat)
	if failure != "" {
		s.publish(ctx, c.UserID, "blueprint.scan_failed", fmt.Sprintf("codebase:%d", c.ID), map[string]any{"codebase_id": c.ID, "name": c.Name, "scan_id": sc.ID, "error": failure, "url": link})
		if st.NotifyFailed && n.Configured() {
			_ = n.Send(ctx, notify.Message{Title: "Scan failed: " + c.Name, Body: failure, Priority: "failure", Tags: []string{"warning"}, Link: link})
		}
		return
	}
	payload := map[string]any{"codebase_id": c.ID, "name": c.Name, "scan_id": sc.ID, "url": link, "trigger": sc.Trigger,
		"files": res.Stats.Files, "lines": res.Stats.Lines, "endpoints": res.Stats.Endpoints, "tables": res.Stats.Tables, "cycles": res.Stats.Cycles}
	s.publish(ctx, c.UserID, "blueprint.scan_finished", fmt.Sprintf("codebase:%d", c.ID), payload)
	if st.NotifyDone && n.Configured() {
		body := fmt.Sprintf("%d files, %s lines, %d endpoints, %d tables", res.Stats.Files, groupDigits(res.Stats.Lines), res.Stats.Endpoints, res.Stats.Tables)
		if res.Stats.Cycles > 0 {
			body += fmt.Sprintf(", %d import loops", res.Stats.Cycles)
		}
		_ = n.Send(ctx, notify.Message{Title: "Scanned " + c.Name, Body: body, Priority: "info", Tags: []string{"mag"}, Link: link})
	}
}

// plainScanErr keeps scan errors short and free of credentials.
func plainScanErr(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, "\n"); i > 0 && len(msg) > 400 {
		msg = msg[:i]
	}
	if len(msg) > 600 {
		msg = msg[:600] + "…"
	}
	return redactURL(msg)
}

func gzipJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err := json.NewEncoder(zw).Encode(v); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---- results, cached ----

// resultCache keeps the last few decoded results in memory: a result is
// read again for every view of a codebase.
type resultCache struct {
	mu    sync.Mutex
	max   int
	order []int64
	items map[int64]*analyze.Result
}

func newResultCache(max int) *resultCache {
	return &resultCache{max: max, items: map[int64]*analyze.Result{}}
}

func (c *resultCache) get(id int64) *analyze.Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.items[id]
	if r != nil {
		c.touch(id)
	}
	return r
}

func (c *resultCache) put(id int64, r *analyze.Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[id]; !ok && len(c.order) >= c.max {
		delete(c.items, c.order[0])
		c.order = c.order[1:]
	}
	c.items[id] = r
	c.touch(id)
}

func (c *resultCache) drop(id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, id)
	for i, x := range c.order {
		if x == id {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
}

func (c *resultCache) touch(id int64) {
	for i, x := range c.order {
		if x == id {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	c.order = append(c.order, id)
}

// result loads one of the person's finished scans.
func (s *Server) result(ctx context.Context, uid, scanID int64) (*analyze.Result, error) {
	blob, err := s.store.ScanResult(ctx, uid, scanID) // also the ownership check
	if err != nil {
		return nil, err
	}
	if r := s.results.get(scanID); r != nil {
		return r, nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	var r analyze.Result
	if err := json.NewDecoder(zr).Decode(&r); err != nil {
		return nil, err
	}
	s.results.put(scanID, &r)
	return &r, nil
}

// removeWork deletes a codebase's clone or unpacked archive.
func (s *Server) removeWork(cid int64) {
	_ = os.RemoveAll(s.workDir(cid))
}

func groupDigits(n int) string {
	str := fmt.Sprint(n)
	if n < 10000 {
		return str
	}
	var b strings.Builder
	for i, ch := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	return b.String()
}
