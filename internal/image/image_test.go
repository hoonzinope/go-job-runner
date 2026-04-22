package image

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/hoonzinope/go-job-runner/internal/config"
)

type fakeSource struct {
	listCandidates func(ctx context.Context, q, prefix string) ([]Candidate, error)
	resolve        func(ctx context.Context, imageRef string) (*Candidate, error)
}

func (s fakeSource) ListCandidates(ctx context.Context, q, prefix string) ([]Candidate, error) {
	if s.listCandidates == nil {
		return nil, nil
	}
	return s.listCandidates(ctx, q, prefix)
}

func (s fakeSource) Resolve(ctx context.Context, imageRef string) (*Candidate, error) {
	if s.resolve == nil {
		return nil, nil
	}
	return s.resolve(ctx, imageRef)
}

func TestResolverPolicies(t *testing.T) {
	r := NewResolver(config.ImageConfig{
		AllowedSources:  []string{"local", "remote"},
		DefaultSource:   "remote",
		AllowedPrefixes: []string{"jobs/"},
	})

	if !r.SourceAllowed("local") || r.SourceAllowed("bogus") {
		t.Fatalf("unexpected source allowance result")
	}
	if got := r.DefaultSource(); got != "remote" {
		t.Fatalf("unexpected default source: %q", got)
	}
	if err := r.ValidateRef(""); err == nil {
		t.Fatal("expected empty image ref error")
	}
	if err := r.ValidateRef("jobs/example:latest"); err != nil {
		t.Fatalf("expected allowed ref, got %v", err)
	}
	if err := r.ValidateRef("tools/example:latest"); err == nil {
		t.Fatal("expected prefix rejection")
	}
	if err := r.ValidatePrefix("jobs/alpha"); err != nil {
		t.Fatalf("expected allowed prefix, got %v", err)
	}
	if err := r.ValidatePrefix("tools/alpha"); err == nil {
		t.Fatal("expected prefix rejection")
	}

	r = NewResolver(config.ImageConfig{AllowedSources: []string{"local"}})
	if got := r.DefaultSource(); got != "local" {
		t.Fatalf("unexpected fallback default source: %q", got)
	}
	r = NewResolver(config.ImageConfig{})
	if got := r.DefaultSource(); got != "local" {
		t.Fatalf("unexpected hard fallback default source: %q", got)
	}
}

func TestResolverListCandidatesAndResolve(t *testing.T) {
	r := NewResolver(config.ImageConfig{
		AllowedSources:  []string{"local", "remote"},
		DefaultSource:   "local",
		AllowedPrefixes: []string{"jobs/"},
	})
	r.local = fakeSource{
		listCandidates: func(ctx context.Context, q, prefix string) ([]Candidate, error) {
			return []Candidate{
				{SourceType: "local", ImageRef: "jobs/app:latest", PullRef: "jobs/app:latest"},
				{SourceType: "local", ImageRef: "tools/app:latest", PullRef: "tools/app:latest"},
			}, nil
		},
		resolve: func(ctx context.Context, imageRef string) (*Candidate, error) {
			return &Candidate{SourceType: "local", ImageRef: imageRef, PullRef: imageRef, Digest: ptrString("sha256:local")}, nil
		},
	}
	r.remote = fakeSource{
		listCandidates: func(ctx context.Context, q, prefix string) ([]Candidate, error) {
			return []Candidate{
				{SourceType: "remote", ImageRef: "jobs/app:latest"},
				{SourceType: "remote", ImageRef: "jobs/side:latest"},
				{SourceType: "remote", ImageRef: "other/app:latest"},
			}, nil
		},
		resolve: func(ctx context.Context, imageRef string) (*Candidate, error) {
			return &Candidate{SourceType: "remote", ImageRef: imageRef, PullRef: "pull/" + imageRef, Digest: ptrString("sha256:remote")}, nil
		},
	}

	items, err := r.ListCandidates(context.Background(), "local", "", "")
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(items) != 1 || items[0].ImageRef != "jobs/app:latest" {
		t.Fatalf("unexpected filtered candidates: %+v", items)
	}

	candidate, err := r.Resolve(context.Background(), "remote", "jobs/app:latest")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if candidate.PullRef != "pull/jobs/app:latest" || candidate.ImageRef != "jobs/app:latest" {
		t.Fatalf("unexpected resolved candidate: %+v", candidate)
	}

	if _, err := r.ListCandidates(context.Background(), "bogus", "", ""); err == nil {
		t.Fatal("expected disallowed source error")
	}
	if _, err := r.ListCandidates(context.Background(), "local", "", "tools/"); err == nil {
		t.Fatal("expected disallowed prefix error")
	}
	if _, err := r.Resolve(context.Background(), "local", "tools/app:latest"); err == nil {
		t.Fatal("expected disallowed image ref error")
	}
}

func TestLocalSourceListCandidatesAndResolve(t *testing.T) {
	dir := t.TempDir()
	writeDockerStub(t, dir)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	src := &LocalSource{}
	items, err := src.ListCandidates(context.Background(), "app", "jobs/")
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("unexpected candidate count: %+v", items)
	}
	if items[0].ImageRef != "jobs/app:latest" || items[0].SourceType != "local" || items[0].PullRef != "jobs/app:latest" {
		t.Fatalf("unexpected candidate: %+v", items[0])
	}
	if items[0].Digest == nil || *items[0].Digest != "sha256:111" {
		t.Fatalf("unexpected digest: %+v", items[0].Digest)
	}

	candidate, err := src.Resolve(context.Background(), "jobs/app:latest")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if candidate.Digest == nil || *candidate.Digest != "sha256:inspect-app" {
		t.Fatalf("unexpected resolved digest: %+v", candidate)
	}

	if _, err := src.Resolve(context.Background(), "missing:latest"); err == nil {
		t.Fatal("expected inspect failure")
	}
}

func TestRemoteSourceListCandidatesResolveAndCache(t *testing.T) {
	var catalogCalls, tagCalls, manifestCalls int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/_catalog":
			catalogCalls++
			if r.URL.Query().Get("page") == "2" {
				writeJSON(t, w, map[string]any{"repositories": []string{"jobs/side", "tools/skip"}})
				return
			}
			w.Header().Set("Link", `</v2/_catalog?page=2>; rel="next"`)
			writeJSON(t, w, map[string]any{"repositories": []string{"jobs/app"}})
		case r.URL.Path == "/v2/jobs/app/tags/list":
			tagCalls++
			writeJSON(t, w, map[string]any{"tags": []string{"latest", "1.0"}})
		case r.URL.Path == "/v2/jobs/side/tags/list":
			tagCalls++
			writeJSON(t, w, map[string]any{"tags": []string{"stable"}})
		case r.URL.Path == "/v2/jobs/app/manifests/latest":
			manifestCalls++
			w.Header().Set("Docker-Content-Digest", "sha256:manifest-digest")
			writeJSON(t, w, map[string]any{"schemaVersion": 2, "config": map[string]any{"digest": "sha256:config-digest"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	src := NewRemoteSource(config.ImageRemoteConfig{Endpoint: srv.URL})
	src.client = srv.Client()

	items, err := src.ListCandidates(context.Background(), "", "jobs/")
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("unexpected remote candidate count: %+v", items)
	}
	if catalogCalls != 2 {
		t.Fatalf("expected catalog pagination to be followed, got %d calls", catalogCalls)
	}
	if tagCalls != 2 {
		t.Fatalf("unexpected tag request count: %d", tagCalls)
	}

	items2, err := src.ListCandidates(context.Background(), "", "jobs/")
	if err != nil {
		t.Fatalf("second list candidates: %v", err)
	}
	if len(items2) != 3 {
		t.Fatalf("unexpected cached candidate count: %+v", items2)
	}
	if tagCalls != 2 {
		t.Fatalf("expected tag cache hit, got %d tag calls", tagCalls)
	}

	candidate, err := src.Resolve(context.Background(), "jobs/app:latest")
	if err != nil {
		t.Fatalf("resolve remote: %v", err)
	}
	if manifestCalls != 1 {
		t.Fatalf("expected one manifest request, got %d", manifestCalls)
	}
	host, _ := url.Parse(srv.URL)
	wantPullRef := host.Host + "/jobs/app:latest"
	if candidate.PullRef != wantPullRef {
		t.Fatalf("unexpected pull ref: got %q want %q", candidate.PullRef, wantPullRef)
	}
	if candidate.Digest == nil || *candidate.Digest != "sha256:manifest-digest" {
		t.Fatalf("unexpected digest: %+v", candidate.Digest)
	}
}

func TestRemoteSourceResolveDigestFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/jobs/app/manifests/latest" {
			http.NotFound(w, r)
			return
		}
		writeJSON(t, w, map[string]any{"config": map[string]any{"digest": "sha256:config-digest"}})
	}))
	defer srv.Close()

	src := NewRemoteSource(config.ImageRemoteConfig{Endpoint: srv.URL})
	src.client = srv.Client()

	candidate, err := src.Resolve(context.Background(), "jobs/app:latest")
	if err != nil {
		t.Fatalf("resolve remote: %v", err)
	}
	if candidate.Digest == nil || *candidate.Digest != "sha256:config-digest" {
		t.Fatalf("unexpected digest fallback: %+v", candidate.Digest)
	}
}

func TestSplitImageRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ref          string
		wantRepo     string
		wantRef      string
		wantIsDigest bool
	}{
		{ref: "jobs/app:latest", wantRepo: "jobs/app", wantRef: "latest", wantIsDigest: false},
		{ref: "jobs/app@sha256:abc", wantRepo: "jobs/app", wantRef: "sha256:abc", wantIsDigest: true},
		{ref: "busybox", wantRepo: "busybox", wantRef: "latest", wantIsDigest: false},
	}

	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			t.Parallel()

			repo, ref, isDigest := splitImageRef(tt.ref)
			if repo != tt.wantRepo || ref != tt.wantRef || isDigest != tt.wantIsDigest {
				t.Fatalf("unexpected split result: got (%q, %q, %v) want (%q, %q, %v)", repo, ref, isDigest, tt.wantRepo, tt.wantRef, tt.wantIsDigest)
			}
		})
	}
}

func TestNextCatalogLink(t *testing.T) {
	t.Parallel()

	base, err := url.Parse("https://example.com/v2/_catalog?n=1000")
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}

	got := nextCatalogLink(base, []string{`</v2/_catalog?page=2>; rel="next"`})
	if got != "https://example.com/v2/_catalog?page=2" {
		t.Fatalf("unexpected next link: %q", got)
	}

	if got := nextCatalogLink(base, nil); got != "" {
		t.Fatalf("expected empty next link, got %q", got)
	}
}

func ptrString(v string) *string {
	return &v
}

func writeDockerStub(t *testing.T, dir string) {
	t.Helper()

	script := `#!/bin/sh
set -eu
if [ "$1" = "image" ] && [ "$2" = "ls" ]; then
  printf '%s\n' \
    "jobs/app:latest	sha256:111" \
    "jobs/side:stable	sha256:222" \
    "tools/app:latest	sha256:333" \
    "<none>:<none>	sha256:none"
  exit 0
fi
if [ "$1" = "image" ] && [ "$2" = "inspect" ]; then
  case "$3" in
    jobs/app:latest)
      printf '%s\n' "sha256:inspect-app"
      exit 0
      ;;
    *)
      exit 1
      ;;
  esac
fi
exit 1
`

	path := filepath.Join(dir, "docker")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write docker stub: %v", err)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("write json: %v", err)
	}
}
