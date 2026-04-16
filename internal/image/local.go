package image

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type LocalSource struct{}

func (s *LocalSource) ListCandidates(ctx context.Context, q, prefix string) ([]Candidate, error) {
	cmd := exec.CommandContext(ctx, "docker", "image", "ls", "--format", "{{.Repository}}:{{.Tag}}\t{{.ID}}")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker image ls: %w", err)
	}

	var candidates []Candidate
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		fields := strings.SplitN(scanner.Text(), "\t", 2)
		if len(fields) != 2 {
			continue
		}
		ref := strings.TrimSpace(fields[0])
		digest := strings.TrimSpace(fields[1])
		if ref == "<none>:<none>" || ref == "" {
			continue
		}
		if prefix != "" && !strings.HasPrefix(ref, prefix) {
			continue
		}
		if q != "" && !strings.Contains(ref, q) {
			continue
		}
		d := digest
		candidates = append(candidates, Candidate{SourceType: "local", ImageRef: ref, Digest: &d})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan docker images: %w", err)
	}
	return candidates, nil
}

func (s *LocalSource) Resolve(ctx context.Context, imageRef string) (*Candidate, error) {
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", imageRef, "--format", "{{.Id}}")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker image inspect %q: %w", imageRef, err)
	}
	digest := strings.TrimSpace(string(out))
	if digest == "" {
		return &Candidate{SourceType: "local", ImageRef: imageRef}, nil
	}
	return &Candidate{SourceType: "local", ImageRef: imageRef, Digest: &digest}, nil
}
