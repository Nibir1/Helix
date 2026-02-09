// internal/rag/vectorstore.go

package rag

import (
	"encoding/json"
	"fmt"
	"helix/internal/shell"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/fatih/color"
)

// -----------------------------------------------------------------------------
// STRUCTS
// -----------------------------------------------------------------------------

type VectorDocument struct {
	ID         string    `json:"id"`
	Content    string    `json:"content"`
	Embedding  []float32 `json:"embedding"` // reserved for future LLM embeddings
	Metadata   Metadata  `json:"metadata"`
	Similarity float32   `json:"similarity,omitempty"`
}

type Metadata struct {
	Command     string   `json:"command"`
	Section     string   `json:"section"`
	Description string   `json:"description"`
	Options     []string `json:"options"`
	Examples    []string `json:"examples"`
}

// VectorStore contains usable vector index and inverted index
type VectorStore struct {
	indexDir    string
	documents   map[string]VectorDocument
	index       map[string][]string // inverted index: word → docIDs
	mu          sync.RWMutex
	initialized bool
}

// Constructor
func NewVectorStore(env shell.Env) *VectorStore {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}

	return &VectorStore{
		indexDir:  filepath.Join(home, ".helix", "vector_index"),
		documents: make(map[string]VectorDocument),
		index:     make(map[string][]string),
	}
}

// -----------------------------------------------------------------------------
// INDEXING
// -----------------------------------------------------------------------------

// IndexMANPages is the ONLY method called by RAG system to index MAN pages.
func (vs *VectorStore) IndexMANPages(pages []MANPage) error {
	color.Blue("Indexing %d MAN pages in vector store...", len(pages))

	if len(pages) == 0 {
		return fmt.Errorf("no MAN pages to index")
	}

	var wg sync.WaitGroup
	docChan := make(chan VectorDocument, len(pages)*3)

	for _, page := range pages {
		wg.Add(1)
		go vs.processPage(page, &wg, docChan)
	}

	go func() {
		wg.Wait()
		close(docChan)
	}()

	count := 0
	for doc := range docChan {
		vs.mu.Lock()
		vs.documents[doc.ID] = doc
		vs.addToIndex(doc)
		vs.mu.Unlock()
		count++
	}

	vs.initialized = true
	color.Green("Vector indexing completed. Documents: %d", count)

	return vs.saveVectorIndex()
}

// processPage → creates multiple docs from each MAN page
func (vs *VectorStore) processPage(page MANPage, wg *sync.WaitGroup, out chan<- VectorDocument) {
	defer wg.Done()

	docs := []VectorDocument{
		vs.docFromCommand(page),
		vs.docFromDescription(page),
		vs.docFromSynopsis(page),
		vs.docFromOptions(page),
		vs.docFromExamples(page),
	}

	for _, d := range docs {
		if d.Content != "" {
			out <- d
		}
	}
}

// -----------------------------------------------------------------------------
// DOC BUILDERS
// -----------------------------------------------------------------------------

func (vs *VectorStore) docFromCommand(page MANPage) VectorDocument {
	return VectorDocument{
		ID:      fmt.Sprintf("%s-command", page.Name),
		Content: fmt.Sprintf("command %s: %s", page.Name, page.Description),
		Metadata: Metadata{
			Command:     page.Name,
			Section:     "command",
			Description: page.Description,
		},
	}
}

func (vs *VectorStore) docFromDescription(page MANPage) VectorDocument {
	if page.Description == "" {
		return VectorDocument{}
	}
	return VectorDocument{
		ID:      fmt.Sprintf("%s-description", page.Name),
		Content: page.Description,
		Metadata: Metadata{
			Command: page.Name,
			Section: "description",
		},
	}
}

func (vs *VectorStore) docFromSynopsis(page MANPage) VectorDocument {
	if page.Synopsis == "" {
		return VectorDocument{}
	}
	return VectorDocument{
		ID:      fmt.Sprintf("%s-synopsis", page.Name),
		Content: page.Synopsis,
		Metadata: Metadata{
			Command: page.Name,
			Section: "synopsis",
		},
	}
}

func (vs *VectorStore) docFromOptions(page MANPage) VectorDocument {
	if len(page.Options) == 0 {
		return VectorDocument{}
	}
	text := strings.Join(page.Options, " | ")
	return VectorDocument{
		ID:      fmt.Sprintf("%s-options", page.Name),
		Content: fmt.Sprintf("options for %s: %s", page.Name, text),
		Metadata: Metadata{
			Command: page.Name,
			Section: "options",
			Options: page.Options,
		},
	}
}

func (vs *VectorStore) docFromExamples(page MANPage) VectorDocument {
	if len(page.Examples) == 0 {
		return VectorDocument{}
	}
	text := strings.Join(page.Examples, " | ")
	return VectorDocument{
		ID:      fmt.Sprintf("%s-examples", page.Name),
		Content: fmt.Sprintf("examples for %s: %s", page.Name, text),
		Metadata: Metadata{
			Command:  page.Name,
			Section:  "examples",
			Examples: page.Examples,
		},
	}
}

// -----------------------------------------------------------------------------
// INVERTED INDEX
// -----------------------------------------------------------------------------

func (vs *VectorStore) addToIndex(doc VectorDocument) {
	words := vs.tokenize(doc.Content)
	for _, w := range words {
		vs.index[w] = append(vs.index[w], doc.ID)
	}
}

func (vs *VectorStore) tokenize(text string) []string {
	text = strings.ToLower(text)
	words := strings.Fields(text)

	var out []string
	for _, w := range words {
		w = strings.Trim(w, `.,!?;:"'()[]{}<>`)
		if len(w) > 2 && !vs.isStopWord(w) {
			out = append(out, w)
		}
	}

	return out
}

func (vs *VectorStore) isStopWord(w string) bool {
	stop := map[string]bool{
		"the": true, "and": true, "for": true, "with": true,
		"this": true, "that": true, "from": true, "are": true,
		"was": true, "were": true, "you": true, "your": true,
	}
	return stop[w]
}

// -----------------------------------------------------------------------------
// SEARCH — TF-IDF + relevance boosts
// -----------------------------------------------------------------------------

func (vs *VectorStore) Search(query string, limit int) ([]VectorDocument, error) {
	if !vs.initialized {
		return nil, fmt.Errorf("vector store not initialized")
	}

	vs.mu.RLock()
	defer vs.mu.RUnlock()

	queryWords := vs.tokenize(query)
	docScores := make(map[string]float32)
	totalDocs := float32(len(vs.documents))

	// TF-IDF scoring
	for _, w := range queryWords {
		docs := vs.index[w]
		if len(docs) == 0 {
			continue
		}

		tf := float32(len(docs)) / totalDocs
		df := float64(len(docs))
		idf := float32(math.Log(float64(totalDocs) / df))

		score := tf * idf
		for _, id := range docs {
			docScores[id] += score
		}
	}

	// Boost for exact command name in query
	queryLower := strings.ToLower(query)
	for id, doc := range vs.documents {
		cmdLower := strings.ToLower(doc.Metadata.Command)
		if strings.Contains(queryLower, cmdLower) {
			docScores[id] += 2.0
		}
	}

	// Convert to list
	var results []VectorDocument
	for id, score := range docScores {
		if score < 0.10 {
			continue
		}
		doc := vs.documents[id]
		doc.Similarity = score
		results = append(results, doc)
	}

	// Sort
	sort.Slice(results, func(i, j int) bool {
		return results[i].Similarity > results[j].Similarity
	})

	if len(results) > limit {
		results = results[:limit]
	}

	color.Green("Found %d relevant documents for '%s'", len(results), query)
	return results, nil
}

// -----------------------------------------------------------------------------
// GET FULL COMMAND INFO — used for ExplainCommand
// -----------------------------------------------------------------------------

type CommandInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Synopsis    string   `json:"synopsis"`
	Options     []string `json:"options"`
	Examples    []string `json:"examples"`
}

func (vs *VectorStore) GetCommandInfo(cmd string) (*CommandInfo, error) {
	if !vs.initialized {
		return nil, fmt.Errorf("vector store not initialized")
	}

	vs.mu.RLock()
	defer vs.mu.RUnlock()

	var info CommandInfo
	info.Name = cmd

	for _, doc := range vs.documents {
		if doc.Metadata.Command != cmd {
			continue
		}

		switch doc.Metadata.Section {
		case "command":
			info.Description = doc.Metadata.Description
		case "synopsis":
			info.Synopsis = doc.Content
		case "options":
			info.Options = append(info.Options, doc.Metadata.Options...)
		case "examples":
			info.Examples = append(info.Examples, doc.Metadata.Examples...)
		}
	}

	if info.Description == "" {
		return nil, fmt.Errorf("no information found for: %s", cmd)
	}

	info.Options = unique(info.Options)
	info.Examples = unique(info.Examples)

	return &info, nil
}

func unique(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range list {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// -----------------------------------------------------------------------------
// RELEVANT COMMANDS FOR RAG RETRIEVAL
// -----------------------------------------------------------------------------

func (vs *VectorStore) GetRelevantCommands(query string, max int) ([]CommandInfo, error) {
	docs, err := vs.Search(query, max*2)
	if err != nil {
		return nil, err
	}

	best := map[string]VectorDocument{}
	for _, d := range docs {
		if existing, ok := best[d.Metadata.Command]; !ok || d.Similarity > existing.Similarity {
			best[d.Metadata.Command] = d
		}
	}

	var results []CommandInfo
	for cmd := range best {
		info, err := vs.GetCommandInfo(cmd)
		if err == nil {
			results = append(results, *info)
		}
		if len(results) >= max {
			break
		}
	}

	return results, nil
}

// -----------------------------------------------------------------------------
// PERSISTENCE
// -----------------------------------------------------------------------------

func (vs *VectorStore) ensureIndexDir() error {
	return os.MkdirAll(vs.indexDir, 0755)
}

func (vs *VectorStore) saveVectorIndex() error {
	if err := vs.ensureIndexDir(); err != nil {
		return err
	}

	tempPath := filepath.Join(vs.indexDir, "vector_index.json.tmp")
	finalPath := filepath.Join(vs.indexDir, "vector_index.json")

	data, err := json.MarshalIndent(vs.documents, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return err
	}

	return os.Rename(tempPath, finalPath)
}

func (vs *VectorStore) loadVectorIndex() error {
	path := filepath.Join(vs.indexDir, "vector_index.json")

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	if err := json.Unmarshal(data, &vs.documents); err != nil {
		return err
	}

	// Rebuild inverted index
	vs.index = make(map[string][]string)
	for _, doc := range vs.documents {
		vs.addToIndex(doc)
	}

	vs.initialized = true
	color.Green("Loaded vector index with %d documents", len(vs.documents))
	return nil
}

func (vs *VectorStore) IsInitialized() bool {
	return vs.initialized
}

// -----------------------------------------------------------------------------
// STATS
// -----------------------------------------------------------------------------

func (vs *VectorStore) GetStats() map[string]interface{} {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	commands := map[string]bool{}
	for _, doc := range vs.documents {
		commands[doc.Metadata.Command] = true
	}

	return map[string]interface{}{
		"total_documents": len(vs.documents),
		"unique_commands": len(commands),
		"index_size":      len(vs.index),
		"initialized":     vs.initialized,
	}
}
