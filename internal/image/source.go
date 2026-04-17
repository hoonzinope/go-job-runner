package image

import (
	"context"
	"fmt"
	"strings"

	"github.com/hoonzinope/go-job-runner/internal/config"
)

type Candidate struct {
	SourceType string  `json:"sourceType"`
	ImageRef   string  `json:"imageRef"`
	PullRef    string  `json:"pullRef,omitempty"`
	Digest     *string `json:"digest,omitempty"`
}

type Source interface {
	ListCandidates(ctx context.Context, q, prefix string) ([]Candidate, error)
	Resolve(ctx context.Context, imageRef string) (*Candidate, error)
}

type Resolver struct {
	local  Source
	remote Source
	cfg    config.ImageConfig
}

func NewResolver(cfg config.ImageConfig) *Resolver {
	return &Resolver{
		local:  &LocalSource{},
		remote: NewRemoteSource(cfg.Remote),
		cfg:    cfg,
	}
}

func (r *Resolver) SourceAllowed(sourceType string) bool {
	for _, allowed := range r.cfg.AllowedSources {
		if allowed == sourceType {
			return true
		}
	}
	return false
}

func (r *Resolver) DefaultSource() string {
	if r.cfg.DefaultSource != "" {
		return r.cfg.DefaultSource
	}
	if len(r.cfg.AllowedSources) > 0 {
		return r.cfg.AllowedSources[0]
	}
	return "local"
}

func (r *Resolver) ValidateRef(imageRef string) error {
	if imageRef == "" {
		return fmt.Errorf("image ref is required")
	}
	if len(r.cfg.AllowedPrefixes) == 0 {
		return nil
	}
	for _, prefix := range r.cfg.AllowedPrefixes {
		if strings.HasPrefix(imageRef, prefix) {
			return nil
		}
	}
	return fmt.Errorf("image ref %q is not allowed by prefix policy", imageRef)
}

func (r *Resolver) ListCandidates(ctx context.Context, sourceType, q, prefix string) ([]Candidate, error) {
	if !r.SourceAllowed(sourceType) {
		return nil, fmt.Errorf("source type %q is not allowed", sourceType)
	}
	if err := r.ValidatePrefix(prefix); err != nil {
		return nil, err
	}
	switch sourceType {
	case "local":
		return r.filterCandidates(r.local.ListCandidates(ctx, q, prefix))
	case "remote":
		return r.filterCandidates(r.remote.ListCandidates(ctx, q, prefix))
	default:
		return nil, fmt.Errorf("unsupported source type %q", sourceType)
	}
}

func (r *Resolver) Resolve(ctx context.Context, sourceType, imageRef string) (*Candidate, error) {
	if err := r.ValidateRef(imageRef); err != nil {
		return nil, err
	}
	if !r.SourceAllowed(sourceType) {
		return nil, fmt.Errorf("source type %q is not allowed", sourceType)
	}
	switch sourceType {
	case "local":
		return r.local.Resolve(ctx, imageRef)
	case "remote":
		return r.remote.Resolve(ctx, imageRef)
	default:
		return nil, fmt.Errorf("unsupported source type %q", sourceType)
	}
}

func (r *Resolver) ValidatePrefix(prefix string) error {
	if prefix == "" || len(r.cfg.AllowedPrefixes) == 0 {
		return nil
	}
	for _, allowed := range r.cfg.AllowedPrefixes {
		if strings.HasPrefix(prefix, allowed) {
			return nil
		}
	}
	return fmt.Errorf("prefix %q is not allowed by prefix policy", prefix)
}

func (r *Resolver) filterCandidates(candidates []Candidate, err error) ([]Candidate, error) {
	if err != nil {
		return nil, err
	}
	if len(r.cfg.AllowedPrefixes) == 0 {
		return candidates, nil
	}
	filtered := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		for _, allowed := range r.cfg.AllowedPrefixes {
			if strings.HasPrefix(candidate.ImageRef, allowed) {
				filtered = append(filtered, candidate)
				break
			}
		}
	}
	return filtered, nil
}
