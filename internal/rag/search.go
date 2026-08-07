// internal/rag/search.go
// Purpose: FTS5 + optional embedding search for the Helix knowledge base.
package rag

import (
	"database/sql"
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strings"

	"helix/internal/ai"
)

// KnowledgeEntry is a unified search result.
type KnowledgeEntry struct {
	SourceType  string  `json:"source_type"`
	SourceID    string  `json:"source_id"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Score       float64 `json:"score"`
}

var ftsQuerySanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// SemanticSearch searches the knowledge base.
func SemanticSearch(db *sql.DB, query string, limit int) ([]KnowledgeEntry, error) {
	results := fts5Search(db, query, limit*2)

	if len(results) == 0 {
		results = keywordSearch(db, query, limit*2)
	}

	if len(results) == 0 {
		return []KnowledgeEntry{}, nil
	}

	// Optional embedding augmentation (OpenAI OR local Ollama).
	if EmbeddingsAvailable() {
		queryEmb, err := ai.GetEmbeddings([]string{query})
		if err == nil && len(queryEmb) > 0 {
			return hybridSearch(db, queryEmb[0], results, limit,
				embeddingModelName(currentEmbeddingBackend()))
		}
	}

	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// fts5Search performs sanitized FTS5 search.
func fts5Search(db *sql.DB, query string, limit int) []KnowledgeEntry {
	sanitized := sanitizeFTSQuery(query)
	if sanitized == "" {
		return keywordSearch(db, query, limit)
	}

	rows, err := db.Query(`
		SELECT source_type, source_id, title, description, rank
		FROM knowledge_fts
		WHERE knowledge_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, sanitized, limit)
	if err != nil {
		return keywordSearch(db, query, limit)
	}
	defer func() { _ = rows.Close() }()

	var results []KnowledgeEntry

	for rows.Next() {
		var k KnowledgeEntry
		var rank float64

		if err := rows.Scan(&k.SourceType, &k.SourceID, &k.Title, &k.Description, &rank); err != nil {
			continue
		}

		k.Score = -rank
		results = append(results, k)
	}

	return results
}

// sanitizeFTSQuery converts arbitrary user input into a safe FTS5 query.
func sanitizeFTSQuery(query string) string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return ""
	}

	cleaned := ftsQuerySanitizer.ReplaceAllString(query, " ")
	tokens := strings.Fields(cleaned)

	quoted := make([]string, 0, len(tokens))

	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" || token == "-" || token == "_" {
			continue
		}

		token = strings.ReplaceAll(token, `"`, ``)
		quoted = append(quoted, `"`+token+`"`)
	}

	if len(quoted) == 0 {
		return ""
	}

	return strings.Join(quoted, " OR ")
}

// keywordSearch performs deterministic LIKE-based fallback search.
func keywordSearch(db *sql.DB, query string, limit int) []KnowledgeEntry {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}

	like := "%" + sanitizeLIKE(query) + "%"

	rows, err := db.Query(`
		SELECT source_type, source_id, title, description, score
		FROM (
			SELECT
				'cve' AS source_type,
				id AS source_id,
				id AS title,
				description AS description,
				1.0 AS score
			FROM cve
			WHERE LOWER(id) LIKE ? ESCAPE '\' OR LOWER(description) LIKE ? ESCAPE '\'

			UNION ALL

			SELECT
				'kev',
				cve_id,
				title,
				notes,
				1.2
			FROM kev
			WHERE LOWER(cve_id) LIKE ? ESCAPE '\'
			   OR LOWER(title) LIKE ? ESCAPE '\'
			   OR LOWER(notes) LIKE ? ESCAPE '\'

			UNION ALL

			SELECT
				'exploit',
				edb_id,
				edb_id,
				description,
				1.0
			FROM exploit
			WHERE LOWER(edb_id) LIKE ? ESCAPE '\'
			   OR LOWER(cve_id) LIKE ? ESCAPE '\'
			   OR LOWER(description) LIKE ? ESCAPE '\'

			UNION ALL

			SELECT
				'mitre',
				technique_id,
				name,
				description,
				1.1
			FROM mitre_technique
			WHERE LOWER(technique_id) LIKE ? ESCAPE '\'
			   OR LOWER(name) LIKE ? ESCAPE '\'
			   OR LOWER(description) LIKE ? ESCAPE '\'
		)
		ORDER BY score DESC, source_type, source_id
		LIMIT ?
	`,
		like, like,
		like, like, like,
		like, like, like,
		like, like, like,
		limit,
	)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	var results []KnowledgeEntry

	for rows.Next() {
		var k KnowledgeEntry

		if err := rows.Scan(&k.SourceType, &k.SourceID, &k.Title, &k.Description, &k.Score); err != nil {
			continue
		}

		results = append(results, k)
	}

	return results
}

// sanitizeLIKE escapes LIKE wildcards.
func sanitizeLIKE(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}

// hybridSearch blends FTS rank and embedding similarity.
// FIX: Added `model string` parameter to prevent cross-backend vector mixing.
func hybridSearch(db *sql.DB, queryEmb []float32, fts []KnowledgeEntry, limit int, model string) ([]KnowledgeEntry, error) {
	combined := append([]KnowledgeEntry(nil), fts...)
	for i := range combined {
		entry := &combined[i]
		ftsScore := 1.0 / (1.0 + float64(i))
		emb := loadEmbedding(db, entry.SourceType, entry.SourceID, model)
		if len(emb) > 0 {
			sim := cosineSimilarity(queryEmb, emb)
			entry.Score = 0.3*ftsScore + 0.7*float64(sim)
		} else {
			entry.Score = ftsScore
		}
	}

	sort.SliceStable(combined, func(i, j int) bool {
		if combined[i].Score != combined[j].Score {
			return combined[i].Score > combined[j].Score
		}

		if combined[i].SourceType != combined[j].SourceType {
			return combined[i].SourceType < combined[j].SourceType
		}

		return combined[i].SourceID < combined[j].SourceID
	})

	if len(combined) > limit {
		combined = combined[:limit]
	}

	return combined, nil
}

// loadEmbedding loads a stored embedding, only when it was produced by the
// same model/backend as the current query embedding.
func loadEmbedding(db *sql.DB, sourceType, sourceID, model string) []float32 {
	var blob []byte
	var storedModel string
	err := db.QueryRow(`
SELECT model, embedding
FROM embeddings
WHERE source_type=? AND source_id=?
`, sourceType, sourceID).Scan(&storedModel, &blob)
	if err != nil {
		return nil
	}
	if model != "" && storedModel != model {
		return nil // never compare vectors from different backends
	}
	var emb []float32
	if err := json.Unmarshal(blob, &emb); err != nil {
		return nil
	}
	return emb
}

// cosineSimilarity computes cosine similarity between two vectors.
func cosineSimilarity(a, b []float32) float32 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	dot, normA, normB := 0.0, 0.0, 0.0

	for i := 0; i < len(a); i++ {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return float32(dot / (math.Sqrt(normA) * math.Sqrt(normB)))
}
