package retention

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hoonzinope/go-job-runner/internal/config"
	"github.com/hoonzinope/go-job-runner/internal/model"
	"github.com/hoonzinope/go-job-runner/internal/store"
)

type Pruner struct {
	store *store.Store
	cfg   config.Config
	now   func() time.Time
}

type Report struct {
	DeletedRuns       int64
	DeletedLogFiles   int
	DeletedArtifacts  int
	TruncatedLogFiles int
	FreedBytes        int64
}

type fileEntry struct {
	path    string
	size    int64
	modTime time.Time
}

func NewPruner(cfg config.Config, st *store.Store) *Pruner {
	return &Pruner{
		store: st,
		cfg:   cfg,
		now:   func() time.Time { return time.Now().UTC() },
	}
}

func (p *Pruner) Run(ctx context.Context) (Report, error) {
	var report Report
	if p == nil || p.store == nil || !p.cfg.Retention.Enabled {
		return report, nil
	}

	runs, _, err := p.store.Runs.ListTerminalBefore(ctx, p.now(), store.Page{Page: 1, Size: 100000})
	if err != nil {
		return report, err
	}

	for i := range runs {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		if deleted, bytes, err := p.pruneRunFiles(&runs[i]); err != nil {
			return report, err
		} else {
			report.DeletedLogFiles += deleted.logs
			report.DeletedArtifacts += deleted.artifacts
			report.TruncatedLogFiles += deleted.truncatedLogs
			report.FreedBytes += bytes
		}
	}

	if p.cfg.Retention.RunHistoryDays > 0 {
		cutoff := p.now().AddDate(0, 0, -p.cfg.Retention.RunHistoryDays)
		for i := range runs {
			if runs[i].FinishedAt == nil || !runs[i].FinishedAt.Before(cutoff) {
				continue
			}
			if deleted, bytes, err := p.removeAllRunFiles(&runs[i]); err != nil {
				return report, err
			} else {
				report.DeletedLogFiles += deleted.logs
				report.DeletedArtifacts += deleted.artifacts
				report.FreedBytes += bytes
			}
		}
		deleted, err := p.store.Runs.DeleteTerminalBefore(ctx, cutoff)
		if err != nil {
			return report, err
		}
		report.DeletedRuns = deleted
	}

	if p.cfg.Retention.MaxTotalStorageBytes > 0 {
		freed, err := p.enforceTotalStorage(ctx, p.cfg.Retention.MaxTotalStorageBytes, runs)
		if err != nil {
			return report, err
		}
		report.FreedBytes += freed
	}

	return report, nil
}

type fileDeleteCounts struct {
	logs          int
	artifacts     int
	truncatedLogs int
}

func (p *Pruner) pruneRunFiles(run *model.Run) (fileDeleteCounts, int64, error) {
	var counts fileDeleteCounts
	var freed int64
	if run.Status == model.RunStatusSuccess && p.cfg.Retention.SuccessLogDays > 0 && olderThan(run.FinishedAt, p.cfg.Retention.SuccessLogDays, p.now()) {
		deleted, bytes, err := removeManagedFile(p.cfg.Store.LogRoot, run.LogPath)
		if err != nil {
			return counts, freed, err
		}
		if deleted {
			counts.logs++
			freed += bytes
		}
	}
	if run.Status != model.RunStatusSuccess && p.cfg.Retention.FailedLogDays > 0 && olderThan(run.FinishedAt, p.cfg.Retention.FailedLogDays, p.now()) {
		deleted, bytes, err := removeManagedFile(p.cfg.Store.LogRoot, run.LogPath)
		if err != nil {
			return counts, freed, err
		}
		if deleted {
			counts.logs++
			freed += bytes
		}
	}
	if p.cfg.Retention.ArtifactDays > 0 && olderThan(run.FinishedAt, p.cfg.Retention.ArtifactDays, p.now()) {
		deleted, bytes, err := removeManagedFile(p.cfg.Store.ArtifactRoot, run.ResultPath)
		if err != nil {
			return counts, freed, err
		}
		if deleted {
			counts.artifacts++
			freed += bytes
		}
	}
	if p.cfg.Retention.MaxLogBytesPerRun > 0 && run.LogPath != nil {
		truncated, bytes, err := truncateManagedFile(p.cfg.Store.LogRoot, *run.LogPath, p.cfg.Retention.MaxLogBytesPerRun)
		if err != nil {
			return counts, freed, err
		}
		if truncated {
			counts.truncatedLogs++
			freed += bytes
		}
	}
	return counts, freed, nil
}

func (p *Pruner) removeAllRunFiles(run *model.Run) (fileDeleteCounts, int64, error) {
	var counts fileDeleteCounts
	var freed int64
	if deleted, bytes, err := removeManagedFile(p.cfg.Store.LogRoot, run.LogPath); err != nil {
		return counts, freed, err
	} else if deleted {
		counts.logs++
		freed += bytes
	}
	if deleted, bytes, err := removeManagedFile(p.cfg.Store.ArtifactRoot, run.ResultPath); err != nil {
		return counts, freed, err
	} else if deleted {
		counts.artifacts++
		freed += bytes
	}
	return counts, freed, nil
}

func (p *Pruner) enforceTotalStorage(ctx context.Context, maxBytes int64, runs []model.Run) (int64, error) {
	total, err := managedStorageBytes(p.cfg.Store.LogRoot, p.cfg.Store.ArtifactRoot)
	if err != nil {
		return 0, err
	}
	if total <= maxBytes {
		return 0, nil
	}
	files := terminalRunFiles(p.cfg.Store.LogRoot, p.cfg.Store.ArtifactRoot, runs)
	sort.Slice(files, func(i, j int) bool {
		if files[i].modTime.Equal(files[j].modTime) {
			return files[i].path < files[j].path
		}
		return files[i].modTime.Before(files[j].modTime)
	})

	var freed int64
	for _, file := range files {
		if ctx.Err() != nil {
			return freed, ctx.Err()
		}
		if total <= maxBytes {
			break
		}
		if err := os.Remove(file.path); err != nil && !os.IsNotExist(err) {
			return freed, fmt.Errorf("remove storage file %q: %w", file.path, err)
		}
		total -= file.size
		freed += file.size
	}
	return freed, nil
}

func olderThan(t *time.Time, days int, now time.Time) bool {
	if t == nil || days <= 0 {
		return false
	}
	return t.Before(now.AddDate(0, 0, -days))
}

func removeManagedFile(root string, path *string) (bool, int64, error) {
	if path == nil || *path == "" {
		return false, 0, nil
	}
	clean, ok := cleanManagedPath(root, *path)
	if !ok {
		return false, 0, nil
	}
	info, err := os.Stat(clean)
	if err != nil {
		if os.IsNotExist(err) {
			return false, 0, nil
		}
		return false, 0, fmt.Errorf("stat file %q: %w", clean, err)
	}
	if info.IsDir() {
		return false, 0, nil
	}
	if err := os.Remove(clean); err != nil && !os.IsNotExist(err) {
		return false, 0, fmt.Errorf("remove file %q: %w", clean, err)
	}
	return true, info.Size(), nil
}

func truncateManagedFile(root, path string, maxBytes int64) (bool, int64, error) {
	clean, ok := cleanManagedPath(root, path)
	if !ok {
		return false, 0, nil
	}
	info, err := os.Stat(clean)
	if err != nil {
		if os.IsNotExist(err) {
			return false, 0, nil
		}
		return false, 0, fmt.Errorf("stat log file %q: %w", clean, err)
	}
	if info.IsDir() || info.Size() <= maxBytes {
		return false, 0, nil
	}
	if err := os.Truncate(clean, maxBytes); err != nil {
		return false, 0, fmt.Errorf("truncate log file %q: %w", clean, err)
	}
	return true, info.Size() - maxBytes, nil
}

func managedStorageBytes(roots ...string) (int64, error) {
	var total int64
	for _, root := range roots {
		if root == "" {
			continue
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			total += info.Size()
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return 0, fmt.Errorf("walk storage root %q: %w", root, err)
		}
	}
	return total, nil
}

func terminalRunFiles(logRoot, artifactRoot string, runs []model.Run) []fileEntry {
	files := make([]fileEntry, 0, len(runs)*2)
	for i := range runs {
		if runFile, ok := statManagedRunFile(logRoot, runs[i].LogPath); ok {
			files = append(files, runFile)
		}
		if runFile, ok := statManagedRunFile(artifactRoot, runs[i].ResultPath); ok {
			files = append(files, runFile)
		}
	}
	return files
}

func statManagedRunFile(root string, path *string) (fileEntry, bool) {
	if path == nil {
		return fileEntry{}, false
	}
	clean, ok := cleanManagedPath(root, *path)
	if !ok {
		return fileEntry{}, false
	}
	info, err := os.Stat(clean)
	if err != nil || info.IsDir() {
		return fileEntry{}, false
	}
	return fileEntry{path: clean, size: info.Size(), modTime: info.ModTime()}, true
}

func cleanManagedPath(root, path string) (string, bool) {
	if root == "" || path == "" {
		return "", false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return absPath, true
}
