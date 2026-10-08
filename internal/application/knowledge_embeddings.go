package application

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strconv"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/embeddings"
	"github.com/persioflexa/harflex/internal/knowledge"
)

var ErrEmbeddingUnavailable = errors.New("local embedding model unavailable")
var ErrEmbeddingProfileChanged = errors.New("local embedding profile changed")

func (s *Service) embeddingProfile(ctx context.Context, workspaceID string) (KnowledgeEmbeddingProfileDTO, error) {
	profile, err := s.store.GetEmbeddingProfile(ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return KnowledgeEmbeddingProfileDTO{}, nil
	}
	if err != nil {
		return KnowledgeEmbeddingProfileDTO{}, safe("read local embedding profile", err)
	}
	progress, err := s.store.VectorProgress(ctx, workspaceID, profile.Fingerprint)
	if err != nil {
		return KnowledgeEmbeddingProfileDTO{}, safe("read local vector progress", err)
	}
	return KnowledgeEmbeddingProfileDTO{
		Configured: true, Kind: string(profile.Kind), BaseURL: profile.BaseURL, Model: profile.Model,
		Fingerprint: profile.Fingerprint,
		Progress: struct {
			Total     int `json:"total"`
			Indexed   int `json:"indexed"`
			Dimension int `json:"dimension"`
		}{progress.Total, progress.Indexed, progress.Dimension},
	}, nil
}

func (s *Service) GetKnowledgeEmbeddingProfile(workspaceID string) (KnowledgeEmbeddingProfileDTO, error) {
	if err := s.beginCall(); err != nil {
		return KnowledgeEmbeddingProfileDTO{}, err
	}
	defer s.endCall()
	if _, err := s.knowledgeWorkspace(workspaceID); err != nil {
		return KnowledgeEmbeddingProfileDTO{}, err
	}
	return s.embeddingProfile(s.ctx, workspaceID)
}

func (s *Service) SaveKnowledgeEmbeddingProfile(in SaveKnowledgeEmbeddingProfileInput) (KnowledgeEmbeddingProfileDTO, error) {
	if err := s.beginCall(); err != nil {
		return KnowledgeEmbeddingProfileDTO{}, err
	}
	defer s.endCall()
	if _, err := s.knowledgeWorkspace(in.WorkspaceID); err != nil {
		return KnowledgeEmbeddingProfileDTO{}, err
	}
	profile, err := embeddings.ValidateProfile(embeddings.Profile{Kind: embeddings.Kind(in.Kind), BaseURL: in.BaseURL, Model: in.Model})
	if err != nil {
		return KnowledgeEmbeddingProfileDTO{}, ErrInvalidInput
	}
	s.embeddingGate.Lock()
	defer s.embeddingGate.Unlock()
	if err := s.store.SaveEmbeddingProfile(s.ctx, in.WorkspaceID, profile); err != nil {
		return KnowledgeEmbeddingProfileDTO{}, safe("save local embedding profile", err)
	}
	return s.embeddingProfile(s.ctx, in.WorkspaceID)
}

// Each explicit call commits at most one small batch. Persisted vectors are the resume checkpoint.
func (s *Service) IndexKnowledgeVectors(in IndexKnowledgeVectorsInput) (KnowledgeEmbeddingProfileDTO, error) {
	if err := s.beginCall(); err != nil {
		return KnowledgeEmbeddingProfileDTO{}, err
	}
	defer s.endCall()
	if in.ExpectedFingerprint == "" {
		return KnowledgeEmbeddingProfileDTO{}, ErrInvalidInput
	}
	if _, err := s.knowledgeWorkspace(in.WorkspaceID); err != nil {
		return KnowledgeEmbeddingProfileDTO{}, err
	}
	s.embeddingGate.Lock()
	defer s.embeddingGate.Unlock()
	profile, err := s.store.GetEmbeddingProfile(s.ctx, in.WorkspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return KnowledgeEmbeddingProfileDTO{}, ErrInvalidInput
	}
	if err != nil {
		return KnowledgeEmbeddingProfileDTO{}, safe("read local embedding profile", err)
	}
	if profile.Fingerprint != in.ExpectedFingerprint {
		return KnowledgeEmbeddingProfileDTO{}, ErrEmbeddingProfileChanged
	}
	chunks, err := s.store.PendingVectorChunks(s.ctx, in.WorkspaceID, profile.Fingerprint, embeddings.MaxBatchSize)
	if err != nil {
		return KnowledgeEmbeddingProfileDTO{}, safe("read pending vector chunks", err)
	}
	if len(chunks) > 0 {
		inputs := make([]string, len(chunks))
		for index, chunk := range chunks {
			inputs[index] = chunk.Content
		}
		vectors, err := embeddings.NewClient(profile).Embed(s.ctx, inputs)
		if err != nil {
			if s.ctx.Err() != nil {
				return KnowledgeEmbeddingProfileDTO{}, s.ctx.Err()
			}
			return KnowledgeEmbeddingProfileDTO{}, ErrEmbeddingUnavailable
		}
		writes := make([]knowledge.VectorWrite, len(chunks))
		for index, chunk := range chunks {
			writes[index] = knowledge.VectorWrite{DocumentID: chunk.DocumentID, Ordinal: chunk.Ordinal, ContentHash: chunk.ContentHash, Values: vectors[index]}
		}
		if err := s.store.SaveVectorBatch(s.ctx, in.WorkspaceID, profile.Fingerprint, writes); err != nil {
			if errors.Is(err, embeddings.ErrInvalidResponse) {
				return KnowledgeEmbeddingProfileDTO{}, ErrEmbeddingUnavailable
			}
			if errors.Is(err, knowledge.ErrVectorSourceChanged) {
				return KnowledgeEmbeddingProfileDTO{}, ErrKnowledgeSourceChanged
			}
			return KnowledgeEmbeddingProfileDTO{}, safe("save local vector batch", err)
		}
	}
	return s.embeddingProfile(s.ctx, in.WorkspaceID)
}

func fuseKnowledgeHits(textHits, vectorHits []catalog.KnowledgeHit, limit int) []catalog.KnowledgeHit {
	type ranked struct {
		hit   catalog.KnowledgeHit
		score float64
	}
	items := make(map[string]ranked, len(textHits)+len(vectorHits))
	add := func(hits []catalog.KnowledgeHit, textual bool) {
		for rank, hit := range hits {
			key := hit.DocumentID + ":" + strconv.Itoa(hit.Ordinal)
			item := items[key]
			if item.hit.DocumentID == "" || textual {
				item.hit = hit
			}
			item.score += 1 / float64(60+rank+1)
			items[key] = item
		}
	}
	add(vectorHits, false)
	add(textHits, true)
	ordered := make([]ranked, 0, len(items))
	for _, item := range items {
		ordered = append(ordered, item)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].score != ordered[j].score {
			return ordered[i].score > ordered[j].score
		}
		if ordered[i].hit.Path != ordered[j].hit.Path {
			return ordered[i].hit.Path < ordered[j].hit.Path
		}
		return ordered[i].hit.Ordinal < ordered[j].hit.Ordinal
	})
	if len(ordered) > limit {
		ordered = ordered[:limit]
	}
	result := make([]catalog.KnowledgeHit, len(ordered))
	for index, item := range ordered {
		result[index] = item.hit
	}
	return result
}

func (s *Service) searchKnowledgeDetailed(ctx context.Context, in SearchKnowledgeInput) (KnowledgeSearchDTO, error) {
	if _, err := s.knowledgeWorkspace(in.WorkspaceID); err != nil {
		return KnowledgeSearchDTO{}, err
	}
	query, err := knowledgeFTSQuery(in.Query)
	if err != nil || in.Limit < 0 || in.Limit > 50 {
		return KnowledgeSearchDTO{}, ErrInvalidInput
	}
	limit := in.Limit
	if limit == 0 {
		limit = 20
	}
	textHits, err := s.store.SearchKnowledge(ctx, in.WorkspaceID, in.DocumentID, query, limit)
	if err != nil {
		return KnowledgeSearchDTO{}, safe("search knowledge", err)
	}
	result := KnowledgeSearchDTO{Mode: "textual", Hits: make([]KnowledgeHitDTO, 0, len(textHits))}
	profile, err := s.store.GetEmbeddingProfile(ctx, in.WorkspaceID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return KnowledgeSearchDTO{}, safe("read local embedding profile", err)
	}
	if err == nil {
		progress, err := s.store.VectorProgress(ctx, in.WorkspaceID, profile.Fingerprint)
		if err != nil {
			return KnowledgeSearchDTO{}, safe("read local vector progress", err)
		}
		if progress.Indexed > 0 {
			vectors, err := embeddings.NewClient(profile).Embed(ctx, []string{in.Query})
			if ctx.Err() != nil {
				return KnowledgeSearchDTO{}, ctx.Err()
			}
			if err == nil && len(vectors[0]) == progress.Dimension {
				current, currentErr := s.store.GetEmbeddingProfile(ctx, in.WorkspaceID)
				if currentErr == nil && current.Fingerprint == profile.Fingerprint {
					vectorHits, searchErr := s.store.SearchVectors(ctx, in.WorkspaceID, in.DocumentID, profile.Fingerprint, vectors[0], limit)
					if ctx.Err() != nil {
						return KnowledgeSearchDTO{}, ctx.Err()
					}
					current, currentErr = s.store.GetEmbeddingProfile(ctx, in.WorkspaceID)
					if currentErr == nil && current.Fingerprint == profile.Fingerprint && searchErr == nil && len(vectorHits) > 0 {
						if len(textHits) > 0 {
							result.Mode = "hybrid"
						} else {
							result.Mode = "semantic"
						}
						textHits = fuseKnowledgeHits(textHits, vectorHits, limit)
					} else if currentErr == nil && current.Fingerprint == profile.Fingerprint && searchErr != nil {
						result.Mode = "textual_fallback"
					}
				} else if currentErr != nil {
					result.Mode = "textual_fallback"
				}
			} else {
				result.Mode = "textual_fallback"
			}
		}
	}
	for _, item := range textHits {
		result.Hits = append(result.Hits, knowledgeHitDTO(item))
	}
	return result, nil
}

func (s *Service) SearchKnowledgeDetailed(in SearchKnowledgeInput) (KnowledgeSearchDTO, error) {
	if err := s.beginCall(); err != nil {
		return KnowledgeSearchDTO{}, err
	}
	defer s.endCall()
	return s.searchKnowledgeDetailed(s.ctx, in)
}
