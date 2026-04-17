package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

type DockerExecutor struct {
	pullPolicy   string
	resolver     *image.Resolver
	logWriter    *logwriter.Writer
	resultWriter *logwriter.ResultWriter
}

func NewDockerExecutor(storeCfg config.StoreConfig, imageCfg config.ImageConfig) *DockerExecutor {
	return &DockerExecutor{
		pullPolicy:   imageCfg.PullPolicy,
		resolver:     image.NewResolver(imageCfg),
		logWriter:    logwriter.NewWriter(storeCfg.LogRoot, storeCfg.LogPathPattern),
		resultWriter: logwriter.NewResultWriter(storeCfg.ArtifactRoot, storeCfg.ResultPathPattern),
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

	pullRef := candidate.PullRef
	if pullRef == "" {
		pullRef = candidate.ImageRef
	}

	if err := e.ensureImage(ctx, pullRef); err != nil {
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
	args := []string{"run", "--rm", "--name", containerName}
	if job.TimeoutSec > 0 {
		args = append(args, "--stop-timeout", strconv.Itoa(job.TimeoutSec))
	}
	if params := jobParamsJSON(job); strings.TrimSpace(params) != "" {
		// Pass parameters into the container, not the docker CLI process.
		args = append(args, "-e", "JOB_PARAMS="+params)
	}
	args = append(args, pullRef)

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start docker run: %w", err)
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
		return &ExecutionResult{
			ExitCode:    -1,
			LogPath:     logPath,
			ResultPath:  resultPath,
			ImageDigest: candidate.Digest,
		}, ctx.Err()
	case err := <-waitCh:
		if err != nil {
			exitCode := exitCodeFromErr(err)
			if exitCode == -1 {
				if writeErr := e.writeResult(resultFile, &logwriter.ResultRecord{
					JobID:       job.ID,
					RunID:       run.ID,
					ImageRef:    candidate.ImageRef,
					PullRef:     candidate.PullRef,
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
				PullRef:     candidate.PullRef,
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
			PullRef:     candidate.PullRef,
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
}

func (e *DockerExecutor) ensureImage(ctx context.Context, imageRef string) error {
	switch strings.ToLower(strings.TrimSpace(e.pullPolicy)) {
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
		return fmt.Errorf("unsupported pull policy %q", e.pullPolicy)
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
