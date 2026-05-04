package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hoonzinope/go-job-runner/internal/config"
	"github.com/hoonzinope/go-job-runner/internal/image"
	logwriter "github.com/hoonzinope/go-job-runner/internal/log"
	"github.com/hoonzinope/go-job-runner/internal/model"
)

const (
	containerNamePrefix   = "job-runner-run"
	containerLabelManaged = "go-job-runner.managed"
	containerLabelApp     = "go-job-runner"
	containerLabelRunID   = "go-job-runner.run-id"
	containerLabelJobID   = "go-job-runner.job-id"
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
	RunContainer(context.Context, containerSpec, io.Writer, io.Writer) error
	CleanupContainer(context.Context, string) error
	RecoverOrphans(context.Context) error
}

type DockerExecutor struct {
	resolver          imageResolver
	runner            containerRunner
	logWriter         *logwriter.Writer
	resultWriter      *logwriter.ResultWriter
	containerNameFunc func(jobID, runID int64) string
}

func NewDockerExecutor(storeCfg config.StoreConfig, imageCfg config.ImageConfig, execCfg config.ExecutorConfig) *DockerExecutor {
	return &DockerExecutor{
		resolver:          image.NewResolver(imageCfg),
		runner:            &realDockerRunner{pullPolicy: imageCfg.PullPolicy, execCfg: execCfg},
		logWriter:         logwriter.NewWriter(storeCfg.LogRoot, storeCfg.LogPathPattern),
		resultWriter:      logwriter.NewResultWriter(storeCfg.ArtifactRoot, storeCfg.ResultPathPattern),
		containerNameFunc: runnerContainerName,
	}
}

func (e *DockerExecutor) RecoverOrphans(ctx context.Context) error {
	if e == nil || e.runner == nil {
		return fmt.Errorf("executor is not configured")
	}
	return e.runner.RecoverOrphans(ctx)
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

	containerName := e.containerNameFunc
	if containerName == nil {
		containerName = runnerContainerName
	}
	name := containerName(run.JobID, run.ID)
	log.Printf("docker executor start run=%d job=%d container=%s image=%s", run.ID, job.ID, name, pullRef)
	if err := e.runner.PrepareContainer(ctx, name); err != nil {
		return nil, err
	}
	spec := containerSpec{
		Name:       name,
		JobID:      job.ID,
		RunID:      run.ID,
		ImageRef:   pullRef,
		TimeoutSec: job.TimeoutSec,
		ParamsJSON: jobParamsJSON(job),
	}
	if err := e.runner.RunContainer(ctx, spec, logFile, logFile); err != nil {
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

type containerSpec struct {
	Name       string
	JobID      int64
	RunID      int64
	ImageRef   string
	TimeoutSec int
	ParamsJSON string
}

type realDockerRunner struct {
	pullPolicy string
	execCfg    config.ExecutorConfig
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
	return r.removeContainer(ctx, containerName, true)
}

func (r *realDockerRunner) CleanupContainer(ctx context.Context, containerName string) error {
	if !r.execCfg.CleanupContainers {
		return nil
	}
	return r.removeContainer(ctx, containerName, true)
}

func (r *realDockerRunner) RecoverOrphans(ctx context.Context) error {
	if !r.execCfg.OrphanRecoveryOnStartup {
		return nil
	}
	out, err := exec.CommandContext(ctx, "docker", "ps", "-aq", "--filter", "label="+containerLabelManaged+"=true").CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(out))
		if trimmed != "" {
			return fmt.Errorf("docker orphan scan: %w: %s", err, trimmed)
		}
		return fmt.Errorf("docker orphan scan: %w", err)
	}
	ids := strings.Fields(string(out))
	for _, id := range ids {
		if err := r.removeContainer(ctx, id, true); err != nil {
			return err
		}
	}
	return nil
}

func (r *realDockerRunner) removeContainer(ctx context.Context, containerName string, force bool) error {
	args := []string{"rm"}
	if force {
		args = append(args, "-f")
	}
	args = append(args, containerName)
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
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

func (r *realDockerRunner) RunContainer(ctx context.Context, spec containerSpec, stdout, stderr io.Writer) error {
	args := dockerRunArgs(spec, r.execCfg)
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	log.Printf("docker container launch run=%d job=%d container=%s image=%s", spec.RunID, spec.JobID, spec.Name, spec.ImageRef)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start docker run: %w", err)
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		log.Printf("docker container stop requested run=%d job=%d container=%s", spec.RunID, spec.JobID, spec.Name)
		stopCtx, cancelStop := context.WithTimeout(context.Background(), r.stopGracePeriod())
		_ = exec.CommandContext(stopCtx, "docker", "stop", "-t", strconv.Itoa(r.stopGracePeriodSec()), spec.Name).Run()
		cancelStop()
		_ = cmd.Process.Kill()
		_ = <-waitCh
		_ = r.CleanupContainer(context.Background(), spec.Name)
		return ctx.Err()
	case err := <-waitCh:
		if err != nil {
			log.Printf("docker container exit run=%d job=%d container=%s err=%v", spec.RunID, spec.JobID, spec.Name, err)
		} else {
			log.Printf("docker container exit run=%d job=%d container=%s", spec.RunID, spec.JobID, spec.Name)
		}
		if cleanupErr := r.CleanupContainer(context.Background(), spec.Name); cleanupErr != nil {
			if err != nil {
				return errors.Join(err, fmt.Errorf("cleanup container %q: %w", spec.Name, cleanupErr))
			}
			return cleanupErr
		}
		return err
	}
}

func (r *realDockerRunner) stopGracePeriodSec() int {
	if r.execCfg.StopGracePeriodSec < 0 {
		return 0
	}
	return r.execCfg.StopGracePeriodSec
}

func (r *realDockerRunner) stopGracePeriod() time.Duration {
	return time.Duration(r.stopGracePeriodSec()+5) * time.Second
}

func dockerRunArgs(spec containerSpec, execCfg config.ExecutorConfig) []string {
	args := []string{
		"run",
		"--name",
		spec.Name,
		"--label",
		containerLabelManaged + "=true",
		"--label",
		containerLabelApp + "=true",
		"--label",
		fmt.Sprintf("%s=%d", containerLabelJobID, spec.JobID),
		"--label",
		fmt.Sprintf("%s=%d", containerLabelRunID, spec.RunID),
	}

	switch strings.ToLower(strings.TrimSpace(execCfg.NetworkMode)) {
	case "", "bridge":
		args = append(args, "--network", "bridge")
	case "none":
		args = append(args, "--network", "none")
	}
	args = append(args, "--security-opt", "no-new-privileges")
	if execCfg.ReadOnlyRootFS {
		args = append(args, "--read-only")
	}
	if execCfg.MemoryLimitMB > 0 {
		args = append(args, "--memory", strconv.Itoa(execCfg.MemoryLimitMB)+"m")
	}
	if execCfg.CPULimit > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(execCfg.CPULimit, 'f', -1, 64))
	}
	if execCfg.StopGracePeriodSec > 0 {
		args = append(args, "--stop-timeout", strconv.Itoa(execCfg.StopGracePeriodSec))
	}
	if strings.TrimSpace(spec.ParamsJSON) != "" {
		args = append(args, "-e", "JOB_PARAMS="+spec.ParamsJSON)
	}
	args = append(args, spec.ImageRef)
	return args
}

func runnerContainerName(jobID, runID int64) string {
	return runnerContainerNameWithSuffix(jobID, runID, uuid.NewString())
}

func runnerContainerNameWithSuffix(jobID, runID int64, suffix string) string {
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		suffix = uuid.NewString()
	}
	return fmt.Sprintf("%s-%d-%d-%s", containerNamePrefix, jobID, runID, suffix)
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
