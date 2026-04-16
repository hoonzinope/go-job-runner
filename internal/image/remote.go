package image

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/hoonzinope/go-job-runner/internal/config"
)

type RemoteSource struct {
	endpoint string
	client   *http.Client
}

func NewRemoteSource(cfg config.ImageRemoteConfig) *RemoteSource {
	return &RemoteSource{
		endpoint: strings.TrimRight(cfg.Endpoint, "/"),
		client:   &http.Client{},
	}
}

func (s *RemoteSource) ListCandidates(ctx context.Context, q, prefix string) ([]Candidate, error) {
	repos, err := s.listRepositories(ctx)
	if err != nil {
		return nil, err
	}

	var candidates []Candidate
	for _, repo := range repos {
		if prefix != "" && !strings.HasPrefix(repo, prefix) {
			continue
		}
		if q != "" && !strings.Contains(repo, q) {
			continue
		}
		tags, err := s.listTags(ctx, repo)
		if err != nil {
			continue
		}
		for _, tag := range tags {
			ref := fmt.Sprintf("%s:%s", repo, tag)
			if q != "" && !strings.Contains(ref, q) {
				continue
			}
			candidates = append(candidates, Candidate{SourceType: "remote", ImageRef: ref})
		}
	}
	return candidates, nil
}

func (s *RemoteSource) Resolve(ctx context.Context, imageRef string) (*Candidate, error) {
	repo, tag := splitImageRef(imageRef)
	if repo == "" || tag == "" {
		return nil, fmt.Errorf("invalid remote image ref %q", imageRef)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint+"/v2/"+repo+"/manifests/"+url.PathEscape(tag), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remote manifest request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("remote manifest status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		var payload map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&payload); err == nil {
			if v, ok := payload["config"].(map[string]any); ok {
				if d, ok := v["digest"].(string); ok {
					digest = d
				}
			}
		}
	}
	if digest == "" {
		return &Candidate{SourceType: "remote", ImageRef: imageRef}, nil
	}
	return &Candidate{SourceType: "remote", ImageRef: imageRef, Digest: &digest}, nil
}

func (s *RemoteSource) listRepositories(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint+"/v2/_catalog?n=1000", nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remote catalog request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("remote catalog status %d", resp.StatusCode)
	}
	var payload struct {
		Repositories []string `json:"repositories"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode remote catalog: %w", err)
	}
	return payload.Repositories, nil
}

func (s *RemoteSource) listTags(ctx context.Context, repo string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint+"/v2/"+repo+"/tags/list", nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remote tags request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("remote tags status %d", resp.StatusCode)
	}
	var payload struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode remote tags: %w", err)
	}
	return payload.Tags, nil
}

func splitImageRef(ref string) (string, string) {
	lastColon := strings.LastIndex(ref, ":")
	lastSlash := strings.LastIndex(ref, "/")
	if lastColon <= lastSlash || lastColon == -1 {
		return ref, "latest"
	}
	return ref[:lastColon], ref[lastColon+1:]
}
