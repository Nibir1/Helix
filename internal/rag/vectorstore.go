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

// VectorDocument represents a document with its vector embedding
type VectorDocument struct {
	ID         string    `json:"id"`
	Content    string    `json:"content"`
	Embedding  []float32 `json:"embedding"`
	Metadata   Metadata  `json:"metadata"`
	Similarity float32   `json:"similarity,omitempty"`
}

// Metadata contains document metadata
type Metadata struct {
	Command     string   `json:"command"`
	Section     string   `json:"section"`
	Description string   `json:"description"`
	Options     []string `json:"options"`
	Examples    []string `json:"examples"`
}

// VectorStore manages document embeddings and similarity search
type VectorStore struct {
	indexDir    string
	documents   map[string]VectorDocument
	index       map[string][]string // word -> document IDs
	mu          sync.RWMutex
	initialized bool
}

// NewVectorStore creates a new vector store
func NewVectorStore(env shell.Env) *VectorStore {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "/tmp"
	}

	indexDir := filepath.Join(homeDir, ".helix", "vector_index")

	return &VectorStore{
		indexDir:  indexDir,
		documents: make(map[string]VectorDocument),
		index:     make(map[string][]string),
	}
}

// IndexMANPages indexes MAN pages in the vector store
func (vs *VectorStore) IndexMANPages(pages []MANPage) error {
	color.Blue("🔧 Indexing %d MAN pages in vector store...", len(pages))

	if len(pages) == 0 {
		color.Red("❌ No MAN pages to index!")
		return fmt.Errorf("no MAN pages provided")
	}

	// Validate pages have content
	validPages := 0
	for i, page := range pages {
		if page.Name == "" || page.Description == "" {
			color.Yellow("⚠️  Page %d has empty content: %+v", i, page)
			continue
		}
		validPages++
	}

	color.Cyan("🔍 DEBUG: %d valid pages out of %d total", validPages, len(pages))

	if validPages == 0 {
		color.Red("❌ No valid MAN pages to index!")
		return fmt.Errorf("no valid MAN pages")
	}

	var wg sync.WaitGroup
	docChan := make(chan VectorDocument, len(pages))

	// Process pages in parallel
	for _, page := range pages {
		wg.Add(1)
		go vs.processMANPage(page, &wg, docChan)
	}

	// Collect documents
	go func() {
		wg.Wait()
		close(docChan)
	}()

	// Index documents
	count := 0
	for doc := range docChan {
		vs.mu.Lock()
		vs.documents[doc.ID] = doc
		vs.addToIndex(doc)
		vs.mu.Unlock()
		count++

		if count%50 == 0 {
			color.Green("✅ Vector indexed %d documents...", count)
		}
	}

	vs.initialized = true
	color.Green("🎉 Vector indexing completed! %d documents indexed", count)

	return vs.saveVectorIndex()
}

// processMANPage converts a MAN page to vector documents
func (vs *VectorStore) processMANPage(page MANPage, wg *sync.WaitGroup, docChan chan<- VectorDocument) {
	defer wg.Done()

	// Create multiple documents from different sections of the MAN page
	documents := []VectorDocument{
		vs.createCommandDocument(page),
		vs.createDescriptionDocument(page),
		vs.createOptionsDocument(page),
		vs.createExamplesDocument(page),
		vs.createSynopsisDocument(page),
	}

	for _, doc := range documents {
		if doc.Content != "" {
			docChan <- doc
		}
	}
}

// createCommandDocument creates a document for command name and basic info
func (vs *VectorStore) createCommandDocument(page MANPage) VectorDocument {
	content := fmt.Sprintf("command %s: %s", page.Name, page.Description)

	return VectorDocument{
		ID:      fmt.Sprintf("%s-command", page.Name),
		Content: content,
		Metadata: Metadata{
			Command:     page.Name,
			Section:     "command",
			Description: page.Description,
		},
	}
}

// createDescriptionDocument creates a document from the description
func (vs *VectorStore) createDescriptionDocument(page MANPage) VectorDocument {
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

// createOptionsDocument creates a document from command options
func (vs *VectorStore) createOptionsDocument(page MANPage) VectorDocument {
	if len(page.Options) == 0 {
		return VectorDocument{}
	}

	optionsText := strings.Join(page.Options, " | ")
	content := fmt.Sprintf("options for %s: %s", page.Name, optionsText)

	return VectorDocument{
		ID:      fmt.Sprintf("%s-options", page.Name),
		Content: content,
		Metadata: Metadata{
			Command: page.Name,
			Section: "options",
			Options: page.Options,
		},
	}
}

// createExamplesDocument creates a document from examples
func (vs *VectorStore) createExamplesDocument(page MANPage) VectorDocument {
	if len(page.Examples) == 0 {
		return VectorDocument{}
	}

	examplesText := strings.Join(page.Examples, " | ")
	content := fmt.Sprintf("examples for %s: %s", page.Name, examplesText)

	return VectorDocument{
		ID:      fmt.Sprintf("%s-examples", page.Name),
		Content: content,
		Metadata: Metadata{
			Command:  page.Name,
			Section:  "examples",
			Examples: page.Examples,
		},
	}
}

// createSynopsisDocument creates a document from synopsis
func (vs *VectorStore) createSynopsisDocument(page MANPage) VectorDocument {
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

// addToIndex adds a document to the inverted index
func (vs *VectorStore) addToIndex(doc VectorDocument) {
	words := vs.tokenize(doc.Content)

	for _, word := range words {
		vs.index[word] = append(vs.index[word], doc.ID)
	}
}

// tokenize splits text into words for indexing
func (vs *VectorStore) tokenize(text string) []string {
	// Convert to lowercase and split
	text = strings.ToLower(text)
	words := strings.Fields(text)

	// Simple stemming and filtering
	var tokens []string
	for _, word := range words {
		// Remove common punctuation
		word = strings.Trim(word, ".,!?;:\"'()[]{}")

		// Filter out very short words and common stop words
		if len(word) > 2 && !vs.isStopWord(word) {
			tokens = append(tokens, word)
		}
	}

	return tokens
}

// isStopWord checks if a word is a common stop word
func (vs *VectorStore) isStopWord(word string) bool {
	stopWords := map[string]bool{
		"the": true, "and": true, "for": true, "with": true, "this": true,
		"that": true, "from": true, "are": true, "was": true, "were": true,
		"have": true, "has": true, "had": true, "will": true, "would": true,
		"could": true, "should": true, "can": true, "may": true, "might": true,
		"which": true, "what": true, "when": true, "where": true, "why": true,
		"how": true, "who": true, "whom": true, "whose": true,
	}

	return stopWords[word]
}

// Search performs semantic search on the vector store
func (vs *VectorStore) Search(query string, limit int) ([]VectorDocument, error) {
	if !vs.initialized {
		return nil, fmt.Errorf("vector store not initialized")
	}

	color.Blue("🔍 Searching for: %s", query)

	vs.mu.RLock()
	defer vs.mu.RUnlock()

	// Enhanced TF-IDF like scoring with better query understanding
	queryWords := vs.tokenize(query)
	docScores := make(map[string]float32)
	totalDocs := float32(len(vs.documents))

	// Calculate scores for each document
	for _, word := range queryWords {
		if docIDs, exists := vs.index[word]; exists {
			// TF (term frequency) - simple count
			tf := float32(len(docIDs)) / totalDocs

			// IDF (inverse document frequency)
			docFreq := float32(len(docIDs))
			idf := float32(1.0)
			if docFreq > 0 {
				idf = float32(math.Log(float64(totalDocs) / float64(docFreq)))
			}

			// TF-IDF score
			score := tf * idf

			for _, docID := range docIDs {
				docScores[docID] += score
			}
		}
	}

	// MAJOR IMPROVEMENT: Add significant bonus for exact command matches
	queryLower := strings.ToLower(query)

	// Check for common patterns in the query and boost relevant commands
	if strings.Contains(queryLower, "list") && strings.Contains(queryLower, "file") {
		// Boost ls, find, dir commands
		for docID, doc := range vs.documents {
			cmdLower := strings.ToLower(doc.Metadata.Command)
			if cmdLower == "ls" || cmdLower == "find" || cmdLower == "dir" {
				docScores[docID] += 3.0
			}
		}
	}

	if strings.Contains(queryLower, "directory") || strings.Contains(queryLower, "folder") {
		// Boost directory-related commands
		for docID, doc := range vs.documents {
			cmdLower := strings.ToLower(doc.Metadata.Command)
			if cmdLower == "ls" || cmdLower == "pwd" || cmdLower == "dir" {
				docScores[docID] += 2.0
			}
		}
	}

	// Add bonus for exact command matches
	if exactDocs := vs.searchExactCommand(query); len(exactDocs) > 0 {
		for _, doc := range exactDocs {
			docScores[doc.ID] += 2.0 // Bonus for exact matches
		}
	}

	// Add bonus for partial matches in command names
	for docID, doc := range vs.documents {
		commandName := strings.ToLower(doc.Metadata.Command)
		queryLower := strings.ToLower(query)

		// Bonus if query contains command name
		if strings.Contains(queryLower, commandName) {
			docScores[docID] += 1.5
		}

		// Bonus if command name contains query words
		for _, word := range queryWords {
			if strings.Contains(commandName, word) {
				docScores[docID] += 0.5
			}
		}
	}

	// NEW: Penalize completely irrelevant commands
	for docID, doc := range vs.documents {
		cmdLower := strings.ToLower(doc.Metadata.Command)
		// Penalize git commands for non-git queries (unless git is mentioned)
		if strings.HasPrefix(cmdLower, "git-") && !strings.Contains(queryLower, "git") {
			docScores[docID] *= 0.1 // Reduce score by 90%
		}
		// Penalize kubectl commands for non-kubernetes queries
		if strings.HasPrefix(cmdLower, "kubectl") && !strings.Contains(queryLower, "kube") {
			docScores[docID] *= 0.1
		}
		// Penalize dangerous commands for safe queries
		if (cmdLower == "killall" || cmdLower == "rm") &&
			!strings.Contains(queryLower, "kill") && !strings.Contains(queryLower, "remove") {
			docScores[docID] *= 0.1
		}
	}

	// Convert to results
	var results []VectorDocument
	for docID, score := range docScores {
		if doc, exists := vs.documents[docID]; exists && score > 0.1 { // Increased threshold
			doc.Similarity = score
			results = append(results, doc)
		}
	}

	// Sort by similarity score (descending)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Similarity > results[j].Similarity
	})

	// Apply limit
	if len(results) > limit {
		results = results[:limit]
	}

	color.Green("✅ Found %d relevant documents for '%s'", len(results), query)

	// Debug: Show top results with better filtering
	if len(results) > 0 {
		color.Cyan("🔍 Top results:")
		for i := 0; i < min(5, len(results)); i++ {
			doc := results[i]
			color.Cyan("  %d. %s (score: %.2f)", i+1, doc.Metadata.Command, doc.Similarity)
		}
	}

	return results, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// searchExactCommand searches for exact command matches
func (vs *VectorStore) searchExactCommand(query string) []VectorDocument {
	var results []VectorDocument
	query = strings.ToLower(strings.TrimSpace(query))

	// Check if query contains a command name
	for _, doc := range vs.documents {
		if strings.ToLower(doc.Metadata.Command) == query {
			results = append(results, doc)
		}
	}

	return results
}

// GetCommandInfo retrieves comprehensive information about a command
func (vs *VectorStore) GetCommandInfo(command string) (*CommandInfo, error) {
	if !vs.initialized {
		return nil, fmt.Errorf("vector store not initialized")
	}

	vs.mu.RLock()
	defer vs.mu.RUnlock()

	var info CommandInfo
	info.Name = command

	// Collect all documents for this command
	for _, doc := range vs.documents {
		if doc.Metadata.Command == command {
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
	}

	// Remove duplicates
	info.Options = vs.removeDuplicates(info.Options)
	info.Examples = vs.removeDuplicates(info.Examples)

	if info.Description == "" {
		return nil, fmt.Errorf("no information found for command: %s", command)
	}

	return &info, nil
}

// CommandInfo contains comprehensive command information
type CommandInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Synopsis    string   `json:"synopsis"`
	Options     []string `json:"options"`
	Examples    []string `json:"examples"`
}

// removeDuplicates removes duplicate strings from a slice
func (vs *VectorStore) removeDuplicates(slice []string) []string {
	seen := make(map[string]bool)
	var result []string

	for _, item := range slice {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}

	return result
}

// GetRelevantCommands finds commands relevant to a user query
func (vs *VectorStore) GetRelevantCommands(query string, maxResults int) ([]CommandInfo, error) {
	docs, err := vs.Search(query, maxResults*2) // Get extra for deduplication
	if err != nil {
		return nil, err
	}

	// Group by command and get best match for each
	commandDocs := make(map[string]VectorDocument)
	for _, doc := range docs {
		current, exists := commandDocs[doc.Metadata.Command]
		if !exists || doc.Similarity > current.Similarity {
			commandDocs[doc.Metadata.Command] = doc
		}
	}

	// Convert to CommandInfo
	var results []CommandInfo
	for command := range commandDocs {
		info, err := vs.GetCommandInfo(command)
		if err == nil {
			results = append(results, *info)
		}

		if len(results) >= maxResults {
			break
		}
	}

	return results, nil
}

// ensureIndexDir creates the index directory
func (vs *VectorStore) ensureIndexDir() error {
	color.Cyan("🔧 Creating vector index directory: %s", vs.indexDir)

	// Create with proper permissions and parents
	if err := os.MkdirAll(vs.indexDir, 0755); err != nil {
		color.Red("❌ Failed to create vector index directory: %v", err)
		return fmt.Errorf("failed to create directory %s: %w", vs.indexDir, err)
	}

	// Verify the directory was created
	if info, err := os.Stat(vs.indexDir); err != nil {
		color.Red("❌ Vector index directory doesn't exist after creation: %v", err)
		return err
	} else if !info.IsDir() {
		color.Red("❌ Vector index path is not a directory: %s", vs.indexDir)
		return fmt.Errorf("path is not a directory: %s", vs.indexDir)
	}

	color.Green("✅ Vector index directory created: %s", vs.indexDir)
	return nil
}

// saveVectorIndex saves the vector index to disk
func (vs *VectorStore) saveVectorIndex() error {
	indexFile := filepath.Join(vs.indexDir, "vector_index.json")
	color.Cyan("💾 Saving vector index to: %s", indexFile)

	// Ensure directory exists
	if err := vs.ensureIndexDir(); err != nil {
		return fmt.Errorf("failed to ensure index directory: %w", err)
	}

	data, err := json.MarshalIndent(vs.documents, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal index: %w", err)
	}

	// Write with temporary file first to avoid corruption
	tempFile := indexFile + ".tmp"
	if err := os.WriteFile(tempFile, data, 0644); err != nil {
		color.Red("❌ Failed to write temporary index file: %v", err)
		return fmt.Errorf("failed to write temporary file: %w", err)
	}

	// Rename to final file (atomic operation)
	if err := os.Rename(tempFile, indexFile); err != nil {
		color.Red("❌ Failed to rename temporary index file: %v", err)
		return fmt.Errorf("failed to rename file: %w", err)
	}

	color.Green("💾 Vector index saved successfully: %s", indexFile)
	color.Green("📊 Index contains %d documents", len(vs.documents))
	return nil
}

// loadVectorIndex loads the vector index from disk
func (vs *VectorStore) loadVectorIndex() error {
	indexFile := filepath.Join(vs.indexDir, "vector_index.json")

	data, err := os.ReadFile(indexFile)
	if err != nil {
		if os.IsNotExist(err) {
			color.Yellow("⚠️  No existing vector index found")
			return nil
		}
		return fmt.Errorf("failed to read index file: %w", err)
	}

	if err := json.Unmarshal(data, &vs.documents); err != nil {
		return fmt.Errorf("failed to unmarshal index: %w", err)
	}

	// Rebuild the inverted index
	vs.index = make(map[string][]string)
	for _, doc := range vs.documents {
		vs.addToIndex(doc)
	}

	vs.initialized = true
	color.Green("✅ Loaded vector index with %d documents", len(vs.documents))
	return nil
}

// IsInitialized returns whether the vector store is ready
func (vs *VectorStore) IsInitialized() bool {
	return vs.initialized
}

// GetStats returns statistics about the vector store
func (vs *VectorStore) GetStats() map[string]interface{} {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	commands := make(map[string]bool)
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
