// internal/rag/search.go
package rag

import (
	"database/sql"
	"encoding/json"
	"math"

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

// SemanticSearch searches the knowledge base. If OpenAI key is available, it computes cosine
// similarity against stored embeddings and merges with FTS5 results. Otherwise, fallback to FTS5.
func SemanticSearch(db *sql.DB, query string, limit int) ([]KnowledgeEntry, error) {
	// FTS5 keyword search
	ftsResults := fts5Search(db, query, limit*2)

	// If embeddings available and online, augment with cosine similarity
	if ai.HasOpenAIKey() {
		queryEmb, err := ai.GetEmbeddings([]string{query})
		if err == nil && len(queryEmb) > 0 {
			return hybridSearch(db, queryEmb[0], ftsResults, limit)
		}
	}
	// Fallback: just return FTS results
	return ftsResults[:min(limit, len(ftsResults))], nil
}

func fts5Search(db *sql.DB, query string, limit int) []KnowledgeEntry {
	rows, err := db.Query(`SELECT source_type, source_id, title, description, rank
		FROM knowledge_fts WHERE knowledge_fts MATCH ? ORDER BY rank LIMIT ?`, query, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var results []KnowledgeEntry
	for rows.Next() {
		var k KnowledgeEntry
		var rank float64
		rows.Scan(&k.SourceType, &k.SourceID, &k.Title, &k.Description, &rank)
		k.Score = rank
		results = append(results, k)
	}
	return results
}

func hybridSearch(db *sql.DB, queryEmb []float32, fts []KnowledgeEntry, limit int) ([]KnowledgeEntry, error) {
	// Retrieve embeddings for all FTS results (plus some random top documents)
	var combined []KnowledgeEntry
	seen := map[string]bool{}
	for _, e := range fts {
		combined = append(combined, e)
		seen[e.SourceType+e.SourceID] = true
	}
	// Also fetch some top documents by global popularity? We'll skip for now.

	// Score by cosine similarity to query embedding, combined with FTS rank.
	for i, entry := range combined {
		emb := loadEmbedding(db, entry.SourceType, entry.SourceID)
		if len(emb) > 0 {
			sim := cosineSimilarity(queryEmb, emb)
			// Blend: weight FTS rank (inverse) and cosine similarity
			ftsScore := 0.0
			if i < len(fts) {
				ftsScore = 1.0 / (1.0 + float64(i))
			}
			entry.Score = 0.3*ftsScore + 0.7*float64(sim)
		}
		combined[i] = entry
	}
	// Sort descending and limit
	// ... (sort and slice)
	return combined[:min(limit, len(combined))], nil
}

func loadEmbedding(db *sql.DB, sourceType, sourceID string) []float32 {
	var blob []byte
	db.QueryRow(`SELECT embedding FROM embeddings WHERE source_type=? AND source_id=?`, sourceType, sourceID).Scan(&blob)
	var emb []float32
	json.Unmarshal(blob, &emb)
	return emb
}

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
