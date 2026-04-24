package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/hoonzinope/go-job-runner/internal/config"
	"github.com/hoonzinope/go-job-runner/internal/image"
	logwriter "github.com/hoonzinope/go-job-runner/internal/log"
	"github.com/hoonzinope/go-job-runner/internal/model"
)

type ExecutionResult struct {
	ExitCode    int
	LogPath     string
	ResultPath  string
	ImageDigest *string
}

type imageResolver interface {
	SourceAllowed(string) bool
	ValidateRef(string) error
	Resolve(context.Context, string, string) (*image.Candidate, error)
}

type containerRunner interface {
	EnsureImage(context.Context, string) error
	PrepareContainer(context.Context, string) error
	RunContainer(context.Context, string, string, int, string, io.Writer, io.Writer) error
}

type DockerExecutor struct {
	resolver     imageResolver
	runner       containerRunner
	logWriter    *logwriter.Writer
	resultWriter *logwriter.ResultWriter
}

func NewDockerExecutor(storeCfg config.StoreConfig, imageCfg config.ImageConfig) *DockerExecutor {
	return &DockerExecutor{
		resolver:     image.NewResolver(imageCfg),
		runner:       &realDockerRunner{pullPolicy: imageCfg.PullPolicy},
		logWriter:    logwriter.NewWriter(storeCfg.LogRoot, storeCfg.LogPathPattern),
		resultWriter: logwriter.NewResultWriter(storeCfg.ArtifactRoot, storeCfg.ResultPathPattern),
	}
}

func (e *DockerExecutor) Execute(ctx context.Context, job *model.Job, run *model.Run) (*ExecutionResult, error) {
	if job == nil || run == nil {
		return nil, fmt.Errorf("job and run are required")
	}
	if e.resolver == nil || e.runner == nil || e.logWriter == nil || e.resultWriter == nil {
		return nil, fmt.Errorf("executor is not configured")
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

	pullRef := candidate.PullRef
	if pullRef == "" {
		pullRef = candidate.ImageRef
	}

	if err := e.runner.EnsureImage(ctx, pullRef); err != nil {
		return nil, err
	}

	logFile, logPath, err := e.logWriter.Open(job.ID, run.ID)
	if err != nil {
		return nil, err
	}
	defer logFile.Close()

	resultFile, resultPath, err := e.resultWriter.Open(job.ID, run.ID)
	if err != nil {
		return nil, err
	}
	defer resultFile.Close()

	containerName := fmt.Sprintf("job-runner-run-%d-%d", run.JobID, run.ID)
	if err := e.runner.PrepareContainer(ctx, containerName); err != nil {
		return nil, err
	}
	if err := e.runner.RunContainer(ctx, containerName, pullRef, job.TimeoutSec, jobParamsJSON(job), logFile, logFile); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return &ExecutionResult{
				ExitCode:    -1,
				LogPath:     logPath,
				ResultPath:  resultPath,
				ImageDigest: candidate.Digest,
			}, err
		}

		exitCode := exitCodeFromErr(err)
		if writeErr := e.writeResult(resultFile, &logwriter.ResultRecord{
			JobID:       job.ID,
			RunID:       run.ID,
			ImageRef:    candidate.ImageRef,
			PullRef:     pullRef,
			ImageDigest: candidate.Digest,
			ExitCode:    exitCode,
			Message:     err.Error(),
			FinishedAt:  time.Now().UTC(),
		}); writeErr != nil {
			return &ExecutionResult{
				ExitCode:    exitCode,
				LogPath:     logPath,
				ResultPath:  resultPath,
				ImageDigest: candidate.Digest,
			}, fmt.Errorf("write result: %w", writeErr)
		}
		return &ExecutionResult{
			ExitCode:    exitCode,
			LogPath:     logPath,
			ResultPath:  resultPath,
			ImageDigest: candidate.Digest,
		}, err
	}

	if writeErr := e.writeResult(resultFile, &logwriter.ResultRecord{
		JobID:       job.ID,
		RunID:       run.ID,
		ImageRef:    candidate.ImageRef,
		PullRef:     pullRef,
		ImageDigest: candidate.Digest,
		ExitCode:    0,
		Message:     "success",
		FinishedAt:  time.Now().UTC(),
	}); writeErr != nil {
		return &ExecutionResult{
			ExitCode:    0,
			LogPath:     logPath,
			ResultPath:  resultPath,
			ImageDigest: candidate.Digest,
		}, fmt.Errorf("write result: %w", writeErr)
	}

	return &ExecutionResult{
		ExitCode:    0,
		LogPath:     logPath,
		ResultPath:  resultPath,
		ImageDigest: candidate.Digest,
	}, nil
}

type realDockerRunner struct {
	pullPolicy string
}

func (r *realDockerRunner) EnsureImage(ctx context.Context, imageRef string) error {
	switch strings.ToLower(strings.TrimSpace(r.pullPolicy)) {
	case "", "if_not_present":
		if err := exec.CommandContext(ctx, "docker", "image", "inspect", imageRef).Run(); err == nil {
			return nil
		}
		fallthrough
	case "always":
		out, err := exec.CommandContext(ctx, "docker", "pull", imageRef).CombinedOutput()
		if err != nil {
			trimmed := strings.TrimSpace(string(out))
			if trimmed != "" {
				return fmt.Errorf("docker pull %q: %w: %s", imageRef, err, trimmed)
			}
			return fmt.Errorf("docker pull %q: %w", imageRef, err)
		}
		return nil
	case "never":
		return nil
	default:
		return fmt.Errorf("unsupported pull policy %q", r.pullPolicy)
	}
}

func (r *realDockerRunner) PrepareContainer(ctx context.Context, containerName string) error {
	out, err := exec.CommandContext(ctx, "docker", "rm", "-f", containerName).CombinedOutput()
	if err == nil {
		return nil
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil
	}
	if strings.Contains(trimmed, "No such container") {
		return nil
	}
	return fmt.Errorf("docker rm -f %q: %w: %s", containerName, err, trimmed)
}

func (r *realDockerRunner) RunContainer(ctx context.Context, containerName, imageRef string, timeoutSec int, paramsJSON string, stdout, stderr io.Writer) error {
	args := []string{"run", "--rm", "--name", containerName}
	if timeoutSec > 0 {
		args = append(args, "--stop-timeout", strconv.Itoa(timeoutSec))
	}
	if strings.TrimSpace(paramsJSON) != "" {
		args = append(args, "-e", "JOB_PARAMS="+paramsJSON)
	}
	args = append(args, imageRef)

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start docker run: %w", err)
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		stopCtx, cancelStop := context.WithTimeout(context.Background(), 10*time.Second)
		_ = exec.CommandContext(stopCtx, "docker", "stop", containerName).Run()
		cancelStop()
		_ = cmd.Process.Kill()
		_ = <-waitCh
		return ctx.Err()
	case err := <-waitCh:
		return err
	}
}

func (e *DockerExecutor) writeResult(resultFile *os.File, record *logwriter.ResultRecord) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if _, err := resultFile.Seek(0, 0); err != nil {
		return err
	}
	if err := resultFile.Truncate(0); err != nil {
		return err
	}
	if _, err := resultFile.Write(data); err != nil {
		return err
	}
	return resultFile.Sync()
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
