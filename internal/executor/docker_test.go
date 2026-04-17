package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoonzinope/go-job-runner/internal/image"
	logwriter "github.com/hoonzinope/go-job-runner/internal/log"
	"github.com/hoonzinope/go-job-runner/internal/model"
)

type fakeResolver struct {
	allowed      bool
	validateErr  error
	resolveErr   error
	candidate    *image.Candidate
	mu           sync.Mutex
	resolveCalls int
}

func (r *fakeResolver) SourceAllowed(string) bool {
	return r.allowed
}

func (r *fakeResolver) ValidateRef(string) error {
	return r.validateErr
}

func (r *fakeResolver) Resolve(context.Context, string, string) (*image.Candidate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resolveCalls++
	if r.resolveErr != nil {
		return nil, r.resolveErr
	}
	if r.candidate == nil {
		return &image.Candidate{ImageRef: "jobs/example:latest"}, nil
	}
	return r.candidate, nil
}

type fakeRunner struct {
	ensureErr error
	runErr    error

	mu            sync.Mutex
	ensureCalls   int
	runCalls      int
	lastContainer string
	lastImageRef  string
	lastTimeout   int
	lastParams    string
	stdoutWrites  string
}

func (r *fakeRunner) EnsureImage(context.Context, string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureCalls++
	return r.ensureErr
}

func (r *fakeRunner) RunContainer(_ context.Context, containerName, imageRef string, timeoutSec int, paramsJSON string, stdout, stderr io.Writer) error {
	r.mu.Lock()
	r.runCalls++
	r.lastContainer = containerName
	r.lastImageRef = imageRef
	r.lastTimeout = timeoutSec
	r.lastParams = paramsJSON
	r.mu.Unlock()

	if r.stdoutWrites != "" {
		_, _ = stdout.Write([]byte(r.stdoutWrites))
	}
	if stderr != nil && stderr != stdout {
		_, _ = stderr.Write([]byte("stderr:" + r.stdoutWrites))
	}
	return r.runErr
}

func TestExecuteHappyPath(t *testing.T) {
	t.Parallel()

	exec := newTestExecutor(t)
	resolver := &fakeResolver{
		allowed: true,
		candidate: &image.Candidate{
			SourceType: "remote",
			ImageRef:   "jobs/example:latest",
			PullRef:    "registry.example/jobs/example:latest",
			Digest:     ptrString("sha256:abc"),
		},
	}
	runner := &fakeRunner{stdoutWrites: "hello from container\n"}
	exec.resolver = resolver
	exec.runner = runner

	job := testExecJob()
	run := testExecRun()

	result, err := exec.Execute(context.Background(), job, run)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.ExitCode != 0 || result.ImageDigest == nil || *result.ImageDigest != "sha256:abc" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if runner.ensureCalls != 1 || runner.runCalls != 1 {
		t.Fatalf("runner call count mismatch: %+v", runner)
	}
	if runner.lastContainer != "job-runner-run-41-42" {
		t.Fatalf("container name mismatch: %q", runner.lastContainer)
	}
	if runner.lastImageRef != "registry.example/jobs/example:latest" {
		t.Fatalf("image ref mismatch: %q", runner.lastImageRef)
	}
	if runner.lastTimeout != 30 {
		t.Fatalf("timeout mismatch: %d", runner.lastTimeout)
	}
	if runner.lastParams != `{"foo":"bar"}` {
		t.Fatalf("params mismatch: %q", runner.lastParams)
	}

	logData, err := os.ReadFile(result.LogPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(logData), "hello from container") {
		t.Fatalf("log content mismatch: %q", string(logData))
	}

	var record logwriter.ResultRecord
	reader := logwriter.NewReader()
	if err := reader.ReadJSON(result.ResultPath, &record); err != nil {
		t.Fatalf("read result: %v", err)
	}
	if record.ExitCode != 0 || record.Message != "success" || record.PullRef != "registry.example/jobs/example:latest" {
		t.Fatalf("unexpected result record: %+v", record)
	}
}

func TestExecuteFallbackToImageRefWhenPullRefEmpty(t *testing.T) {
	t.Parallel()

	exec := newTestExecutor(t)
	exec.resolver = &fakeResolver{
		allowed: true,
		candidate: &image.Candidate{
			SourceType: "local",
			ImageRef:   "jobs/example:latest",
		},
	}
	runner := &fakeRunner{}
	exec.runner = runner

	_, err := exec.Execute(context.Background(), testExecJob(), testExecRun())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if runner.lastImageRef != "jobs/example:latest" {
		t.Fatalf("expected image ref fallback, got %q", runner.lastImageRef)
	}
}

func TestExecuteValidationAndConfigFailures(t *testing.T) {
	t.Parallel()

	exec := newTestExecutor(t)
	exec.runner = &fakeRunner{}
	exec.resolver = &fakeResolver{allowed: false}

	if _, err := exec.Execute(context.Background(), nil, testExecRun()); err == nil {
		t.Fatal("expected error for nil job")
	}
	if _, err := exec.Execute(context.Background(), testExecJob(), nil); err == nil {
		t.Fatal("expected error for nil run")
	}

	job := testExecJob()
	job.SourceType = model.JobSourceTypeRemote
	if _, err := exec.Execute(context.Background(), job, testExecRun()); err == nil {
		t.Fatal("expected source type error")
	}

	exec.resolver = &fakeResolver{allowed: true, validateErr: errors.New("bad ref")}
	if _, err := exec.Execute(context.Background(), testExecJob(), testExecRun()); err == nil {
		t.Fatal("expected ref error")
	}

	exec.resolver = &fakeResolver{allowed: true, resolveErr: errors.New("resolve failed")}
	if _, err := exec.Execute(context.Background(), testExecJob(), testExecRun()); err == nil {
		t.Fatal("expected resolve error")
	}

	exec.resolver = &fakeResolver{
		allowed: true,
		candidate: &image.Candidate{
			SourceType: "local",
			ImageRef:   "jobs/example:latest",
		},
	}
	exec.runner = &fakeRunner{ensureErr: errors.New("pull failed")}
	if _, err := exec.Execute(context.Background(), testExecJob(), testExecRun()); err == nil {
		t.Fatal("expected ensure error")
	}
}

func TestExecuteRunFailures(t *testing.T) {
	t.Parallel()

	t.Run("exit error", func(t *testing.T) {
		exec := newTestExecutor(t)
		exec.resolver = &fakeResolver{
			allowed: true,
			candidate: &image.Candidate{
				SourceType: "local",
				ImageRef:   "jobs/example:latest",
			},
		}
		exec.runner = &fakeRunner{runErr: shellExitError(t, 7)}

		result, err := exec.Execute(context.Background(), testExecJob(), testExecRun())
		if err == nil {
			t.Fatal("expected run error")
		}
		if result.ExitCode != 7 {
			t.Fatalf("exit code mismatch: %d", result.ExitCode)
		}
		record := readResultRecord(t, result.ResultPath)
		if record.ExitCode != 7 {
			t.Fatalf("result exit code mismatch: %+v", record)
		}
		if !strings.Contains(record.Message, "exit status 7") {
			t.Fatalf("unexpected error message: %q", record.Message)
		}
	})

	t.Run("generic error", func(t *testing.T) {
		exec := newTestExecutor(t)
		exec.resolver = &fakeResolver{
			allowed: true,
			candidate: &image.Candidate{
				SourceType: "local",
				ImageRef:   "jobs/example:latest",
			},
		}
		exec.runner = &fakeRunner{runErr: errors.New("docker exploded")}

		result, err := exec.Execute(context.Background(), testExecJob(), testExecRun())
		if err == nil {
			t.Fatal("expected run error")
		}
		if result.ExitCode != -1 {
			t.Fatalf("exit code mismatch: %d", result.ExitCode)
		}
		record := readResultRecord(t, result.ResultPath)
		if record.ExitCode != -1 || record.Message != "docker exploded" {
			t.Fatalf("unexpected result record: %+v", record)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		exec := newTestExecutor(t)
		exec.resolver = &fakeResolver{
			allowed: true,
			candidate: &image.Candidate{
				SourceType: "local",
				ImageRef:   "jobs/example:latest",
			},
		}
		exec.runner = &fakeRunner{runErr: context.Canceled}

		result, err := exec.Execute(context.Background(), testExecJob(), testExecRun())
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancel error, got: %v", err)
		}
		if result.ExitCode != -1 {
			t.Fatalf("exit code mismatch: %d", result.ExitCode)
		}
		info, statErr := os.Stat(result.ResultPath)
		if statErr != nil {
			t.Fatalf("stat result: %v", statErr)
		}
		if info.Size() != 0 {
			t.Fatalf("expected empty result file on cancel, got size %d", info.Size())
		}
	})
}

func TestExitCodeFromErr(t *testing.T) {
	t.Parallel()

	if got := exitCodeFromErr(nil); got != 0 {
		t.Fatalf("nil mismatch: %d", got)
	}
	if got := exitCodeFromErr(errors.New("plain error")); got != -1 {
		t.Fatalf("plain error mismatch: %d", got)
	}
	if got := exitCodeFromErr(shellExitError(t, 13)); got != 13 {
		t.Fatalf("exit error mismatch: %d", got)
	}
}

func TestExecuteMisconfiguredExecutor(t *testing.T) {
	t.Parallel()

	exec := &DockerExecutor{}
	if _, err := exec.Execute(context.Background(), testExecJob(), testExecRun()); err == nil {
		t.Fatal("expected misconfiguration error")
	}
}

func newTestExecutor(t *testing.T) *DockerExecutor {
	t.Helper()

	root := t.TempDir()
	return &DockerExecutor{
		logWriter:    logwriter.NewWriter(filepath.Join(root, "logs"), ""),
		resultWriter: logwriter.NewResultWriter(filepath.Join(root, "artifacts"), ""),
	}
}

func testExecJob() *model.Job {
	params := `{"foo":"bar"}`
	return &model.Job{
		ID:           41,
		Name:         "job-41",
		Enabled:      true,
		SourceType:   model.JobSourceTypeLocal,
		ImageRef:     "jobs/example:latest",
		ScheduleType: model.ScheduleTypeCron,
		Timezone:     "UTC",
		TimeoutSec:   30,
		ParamsJSON:   &params,
	}
}

func testExecRun() *model.Run {
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	return &model.Run{
		ID:          42,
		JobID:       41,
		ScheduledAt: now,
		Status:      model.RunStatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func readResultRecord(t *testing.T, path string) logwriter.ResultRecord {
	t.Helper()

	var record logwriter.ResultRecord
	if err := logwriter.NewReader().ReadJSON(path, &record); err != nil {
		t.Fatalf("read result: %v", err)
	}
	return record
}

func shellExitError(t *testing.T, code int) error {
	t.Helper()

	cmd := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code))
	return cmd.Run()
}

func ptrString(v string) *string {
	return &v
}
