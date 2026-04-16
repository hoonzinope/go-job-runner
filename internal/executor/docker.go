package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hoonzinope/go-job-runner/internal/config"
	"github.com/hoonzinope/go-job-runner/internal/image"
	"github.com/hoonzinope/go-job-runner/internal/model"
)

type ExecutionResult struct {
	ExitCode    int
	LogPath     string
	ResultPath  string
	ImageDigest *string
}

type DockerExecutor struct {
	logRoot      string
	artifactRoot string
	pullPolicy   string
	resolver     *image.Resolver
}

func NewDockerExecutor(storeCfg config.StoreConfig, imageCfg config.ImageConfig) *DockerExecutor {
	return &DockerExecutor{
		logRoot:      storeCfg.LogRoot,
		artifactRoot: storeCfg.ArtifactRoot,
		pullPolicy:   imageCfg.PullPolicy,
		resolver:     image.NewResolver(imageCfg),
	}
}

func (e *DockerExecutor) Execute(ctx context.Context, job *model.Job, run *model.Run) (*ExecutionResult, error) {
	if job == nil || run == nil {
		return nil, fmt.Errorf("job and run are required")
	}
	if !e.resolver.SourceAllowed(string(job.SourceType)) {
		return nil, fmt.Errorf("source type %q is not allowed", job.SourceType)
	}
	if err := e.resolver.ValidateRef(job.ImageRef); err != nil {
		return nil, err
	}

	candidate, err := e.resolver.Resolve(ctx, string(job.SourceType), job.ImageRef)
	if err != nil {
		return nil, err
	}

	if err := e.ensureImage(ctx, candidate.ImageRef); err != nil {
		return nil, err
	}

	logPath, resultPath, err := e.preparePaths(job, run)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(resultPath), 0o755); err != nil {
		return nil, fmt.Errorf("create artifact dir: %w", err)
	}

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	defer logFile.Close()

	containerName := fmt.Sprintf("job-runner-run-%d-%d", run.JobID, run.ID)
	args := []string{"run", "--rm", "--name", containerName}
	if job.TimeoutSec > 0 {
		args = append(args, "--stop-timeout", strconv.Itoa(job.TimeoutSec))
	}
	args = append(args, candidate.ImageRef)

	cmd := exec.Command("docker", args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if params := jobParamsJSON(job); strings.TrimSpace(params) != "" {
		// Jobs are image-driven; params are made available as env vars for the container.
		// The executor keeps this simple by serializing them into a single env var.
		cmd.Env = append(os.Environ(), "JOB_PARAMS="+params)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start docker run: %w", err)
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		_ = exec.Command("docker", "stop", containerName).Run()
		_ = cmd.Process.Kill()
		_ = <-waitCh
		return nil, ctx.Err()
	case err := <-waitCh:
		if err != nil {
			exitCode := exitCodeFromErr(err)
			if exitCode == -1 {
				return &ExecutionResult{
					ExitCode:    exitCode,
					LogPath:     logPath,
					ResultPath:  resultPath,
					ImageDigest: candidate.Digest,
				}, err
			}
			_ = e.writeResult(resultPath, run, job, candidate, exitCode, err.Error())
			return &ExecutionResult{
				ExitCode:    exitCode,
				LogPath:     logPath,
				ResultPath:  resultPath,
				ImageDigest: candidate.Digest,
			}, err
		}
		_ = e.writeResult(resultPath, run, job, candidate, 0, "success")
		return &ExecutionResult{
			ExitCode:    0,
			LogPath:     logPath,
			ResultPath:  resultPath,
			ImageDigest: candidate.Digest,
		}, nil
	}
}

func (e *DockerExecutor) ensureImage(ctx context.Context, imageRef string) error {
	switch strings.ToLower(strings.TrimSpace(e.pullPolicy)) {
	case "", "if_not_present":
		if err := exec.CommandContext(ctx, "docker", "image", "inspect", imageRef).Run(); err == nil {
			return nil
		}
		fallthrough
	case "always":
		return exec.CommandContext(ctx, "docker", "pull", imageRef).Run()
	case "never":
		return nil
	default:
		return fmt.Errorf("unsupported pull policy %q", e.pullPolicy)
	}
}

func (e *DockerExecutor) preparePaths(job *model.Job, run *model.Run) (string, string, error) {
	base := filepath.Join(e.logRoot, fmt.Sprintf("job-%d", job.ID), fmt.Sprintf("run-%d", run.ID))
	logPath := filepath.Join(base, "run.log")
	resultPath := filepath.Join(e.artifactRoot, fmt.Sprintf("job-%d", job.ID), fmt.Sprintf("run-%d", run.ID), "result.json")
	return logPath, resultPath, nil
}

func (e *DockerExecutor) writeResult(resultPath string, run *model.Run, job *model.Job, candidate *image.Candidate, exitCode int, message string) error {
	if err := os.MkdirAll(filepath.Dir(resultPath), 0o755); err != nil {
		return err
	}
	payload := map[string]any{
		"jobId":       job.ID,
		"runId":       run.ID,
		"imageRef":    candidate.ImageRef,
		"imageDigest": candidate.Digest,
		"exitCode":    exitCode,
		"message":     message,
		"finishedAt":  time.Now().UTC().Format(time.RFC3339Nano),
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(resultPath, data, 0o644)
}

func exitCodeFromErr(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return -1
	}
	return exitErr.ExitCode()
}

func jobParamsJSON(j *model.Job) string {
	if j == nil || j.ParamsJSON == nil {
		return ""
	}
	return *j.ParamsJSON
}
