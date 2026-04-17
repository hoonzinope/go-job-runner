package image

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hoonzinope/go-job-runner/internal/config"
)

type RemoteSource struct {
	endpoint   string
	pullPrefix string
	client     *http.Client
	cacheMu    sync.RWMutex
	tagCache   map[string]tagCacheEntry
	cacheTTL   time.Duration
}

type tagCacheEntry struct {
	tags      []string
	fetchedAt time.Time
}

func NewRemoteSource(cfg config.ImageRemoteConfig) *RemoteSource {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.Insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // nolint:gosec
	}
	parsed, err := url.Parse(strings.TrimRight(cfg.Endpoint, "/"))
	pullPrefix := strings.TrimRight(cfg.Endpoint, "/")
	if err == nil && parsed.Host != "" {
		pullPrefix = parsed.Host
	}
	return &RemoteSource{
		endpoint:   strings.TrimRight(cfg.Endpoint, "/"),
		pullPrefix: pullPrefix,
		client: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
		tagCache: make(map[string]tagCacheEntry),
		cacheTTL: 5 * time.Minute,
	}
}

func (s *RemoteSource) ListCandidates(ctx context.Context, q, prefix string) ([]Candidate, error) {
	repos, err := s.listRepositories(ctx)
	if err != nil {
		return nil, err
	}

	filteredRepos := make([]string, 0, len(repos))
	for _, repo := range repos {
		if prefix != "" && !strings.HasPrefix(repo, prefix) {
			continue
		}
		if q != "" && !strings.Contains(repo, q) {
			continue
		}
		filteredRepos = append(filteredRepos, repo)
	}

	var candidates []Candidate

	type repoTags struct {
		items []Candidate
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := make(chan struct{}, 8)
	results := make(chan repoTags, len(filteredRepos))
	errCh := make(chan error, 1)
	var wg sync.WaitGroup

	for _, repo := range filteredRepos {
		repo := repo
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			tags, err := s.tagsForRepo(ctx, repo)
			if err != nil {
				select {
				case errCh <- err:
				default:
				}
				cancel()
				return
			}
			items := make([]Candidate, 0, len(tags))
			select {
			case <-ctx.Done():
				return
			default:
			}
			for _, tag := range tags {
				ref := fmt.Sprintf("%s:%s", repo, tag)
				if q != "" && !strings.Contains(ref, q) {
					continue
				}
				items = append(items, Candidate{SourceType: "remote", ImageRef: ref})
			}
			select {
			case results <- repoTags{items: items}:
			case <-ctx.Done():
			}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	for {
		select {
		case err := <-errCh:
			if err != nil {
				return nil, err
			}
		case item, ok := <-results:
			if !ok {
				select {
				case err := <-errCh:
					if err != nil {
						return nil, err
					}
				default:
				}
				return candidates, nil
			}
			candidates = append(candidates, item.items...)
		case <-ctx.Done():
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return candidates, nil
		}
	}
}

func (s *RemoteSource) Resolve(ctx context.Context, imageRef string) (*Candidate, error) {
	repo, ref, isDigest := splitImageRef(imageRef)
	if repo == "" || ref == "" {
		return nil, fmt.Errorf("invalid remote image ref %q", imageRef)
	}
	var pullRef string
	if isDigest {
		pullRef = fmt.Sprintf("%s/%s@%s", s.pullPrefix, repo, ref)
	} else {
		pullRef = fmt.Sprintf("%s/%s:%s", s.pullPrefix, repo, ref)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint+"/v2/"+repo+"/manifests/"+url.PathEscape(ref), nil)
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read remote manifest body: %w", err)
	}

	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err == nil {
			if v, ok := payload["config"].(map[string]any); ok {
				if d, ok := v["digest"].(string); ok {
					digest = d
				}
			}
		}
	}
	if digest == "" {
		sum := sha256.Sum256(body)
		digest = "sha256:" + hex.EncodeToString(sum[:])
	}
	return &Candidate{SourceType: "remote", ImageRef: imageRef, PullRef: pullRef, Digest: &digest}, nil
}

func (s *RemoteSource) listRepositories(ctx context.Context) ([]string, error) {
	var repos []string
	nextURL := s.endpoint + "/v2/_catalog?n=1000"

	for nextURL != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, nextURL, nil)
		if err != nil {
			return nil, err
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("remote catalog request: %w", err)
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read remote catalog: %w", readErr)
		}
		if resp.StatusCode != http.StatusOK {
			trimmed := body
			if len(trimmed) > 512 {
				trimmed = trimmed[:512]
			}
			return nil, fmt.Errorf("remote catalog status %d: %s", resp.StatusCode, strings.TrimSpace(string(trimmed)))
		}
		var payload struct {
			Repositories []string `json:"repositories"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("decode remote catalog: %w", err)
		}
		repos = append(repos, payload.Repositories...)
		nextURL = nextCatalogLink(resp.Request.URL, resp.Header.Values("Link"))
	}
	return repos, nil
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
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("remote tags status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode remote tags: %w", err)
	}
	return payload.Tags, nil
}

func (s *RemoteSource) tagsForRepo(ctx context.Context, repo string) ([]string, error) {
	now := time.Now().UTC()

	s.cacheMu.RLock()
	if entry, ok := s.tagCache[repo]; ok && now.Sub(entry.fetchedAt) < s.cacheTTL {
		tags := append([]string(nil), entry.tags...)
		s.cacheMu.RUnlock()
		return tags, nil
	}
	s.cacheMu.RUnlock()

	tags, err := s.listTags(ctx, repo)
	if err != nil {
		return nil, err
	}

	s.cacheMu.Lock()
	s.tagCache[repo] = tagCacheEntry{
		tags:      append([]string(nil), tags...),
		fetchedAt: now,
	}
	s.cacheMu.Unlock()
	return tags, nil
}

func splitImageRef(ref string) (string, string, bool) {
	if repo, digest, ok := strings.Cut(ref, "@"); ok {
		return repo, digest, true
	}
	lastColon := strings.LastIndex(ref, ":")
	lastSlash := strings.LastIndex(ref, "/")
	if lastColon <= lastSlash || lastColon == -1 {
		return ref, "latest", false
	}
	return ref[:lastColon], ref[lastColon+1:], false
}

func nextCatalogLink(base *url.URL, values []string) string {
	for _, value := range values {
		parts := strings.Split(value, ",")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if !strings.Contains(part, `rel="next"`) {
				continue
			}
			start := strings.Index(part, "<")
			end := strings.Index(part, ">")
			if start >= 0 && end > start+1 {
				next, err := url.Parse(part[start+1 : end])
				if err != nil {
					return ""
				}
				if base != nil {
					return base.ResolveReference(next).String()
				}
				return next.String()
			}
		}
	}
	return ""
}
