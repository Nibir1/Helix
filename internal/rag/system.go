package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"helix/internal/shell"
	"helix/internal/utils"

	"github.com/fatih/color"
)

// Add these constants for state management
const (
	stateFileName   = "rag_state.json"
	indexVersion    = "1.0"
	maxIndexingTime = 5 * time.Minute // Increased from 2 minutes to 5 minutes
)

// SystemState tracks RAG system persistence
type SystemState struct {
	Version       string    `json:"version"`
	Initialized   bool      `json:"initialized"`
	IndexedTime   time.Time `json:"indexed_time"`
	TotalPages    int       `json:"total_pages"`
	TotalCommands int       `json:"total_commands"`
}

// RAGSystem orchestrates the complete RAG pipeline
type RAGSystem struct {
	env         shell.Env
	indexer     *MANIndexer
	vectorStore *VectorStore
	initialized bool
	indexDir    string
	stateFile   string
}

// NewSystem creates a new RAG system
func NewSystem(env shell.Env) *RAGSystem {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "/tmp"
	}

	indexDir := filepath.Join(homeDir, ".helix", "rag_index")
	stateFile := filepath.Join(indexDir, stateFileName)

	return &RAGSystem{
		env:         env,
		indexDir:    indexDir,
		stateFile:   stateFile,
		indexer:     NewMANIndexer(env),
		vectorStore: NewVectorStore(env),
	}
}

// Initialize sets up the RAG system with proper persistence
func (rs *RAGSystem) Initialize() error {
	color.Cyan("🚀 Initializing RAG System...")

	if err := rs.ensureIndexDir(); err != nil {
		return fmt.Errorf("failed to create RAG index directory: %w", err)
	}

	// FIRST: Try to load existing state
	if rs.loadSystemState() {
		color.Green("✅ RAG system loaded from existing state")
		return nil
	}

	// SECOND: Try to load existing vector index
	if rs.tryLoadExistingIndex() {
		color.Green("✅ RAG system loaded from existing index")
		rs.saveSystemState()
		return nil
	}

	// ONLY if no existing state: Index MAN pages with strict timeout
	color.Blue("📚 Starting MAN page indexing (first time setup)...")
	startTime := time.Now()

	// Strict timeout - don't block startup
	ctx, cancel := context.WithTimeout(context.Background(), maxIndexingTime)
	defer cancel()

	// Progress tracking
	progressTicker := time.NewTicker(10 * time.Second)
	defer progressTicker.Stop()

	indexingCompleted := make(chan bool, 1)

	go func() {
		for {
			select {
			case <-progressTicker.C:
				if rs.initialized {
					return
				}
				elapsed := time.Since(startTime)
				indexedCount := rs.indexer.GetIndexedCount()
				color.Yellow("🔄 RAG indexing... %d pages (%v elapsed)", indexedCount, utils.FormatDuration(elapsed))

				// Show estimated time remaining for longer operations
				if indexedCount > 100 {
					pagesPerSecond := float64(indexedCount) / elapsed.Seconds()
					estimatedTotal := 500 // Conservative estimate
					remainingTime := time.Duration(float64(estimatedTotal-indexedCount)/pagesPerSecond) * time.Second
					if remainingTime > 0 {
						color.Cyan("   Estimated time remaining: %v", utils.FormatDuration(remainingTime))
					}
				}
			case <-ctx.Done():
				// Only show timeout message if indexing hasn't completed
				select {
				case <-indexingCompleted:
					return // Indexing completed, no timeout message needed
				default:
					indexedCount := rs.indexer.GetIndexedCount()
					if indexedCount > 0 {
						color.Yellow("⏰ RAG indexing timeout after %v", utils.FormatDuration(time.Since(startTime)))
						color.Yellow("💡 Using %d partially indexed pages", indexedCount)
					} else {
						color.Yellow("⏰ RAG indexing timeout - no pages indexed")
					}
					return
				}
			}
		}
	}()

	// Run indexing with timeout
	done := make(chan error, 1)
	go func() {
		if err := rs.indexer.IndexAvailableManPages(); err != nil {
			done <- err
			return
		}
		done <- nil
	}()

	var indexingErr error
	select {
	case indexingErr = <-done:
		// Indexing completed (success or error)
		indexingCompleted <- true // Signal that indexing completed
		indexedCount := rs.indexer.GetIndexedCount()
		if indexingErr != nil {
			color.Yellow("⚠️  MAN page indexing had issues: %v", indexingErr)
		}

		if indexedCount > 0 {
			color.Green("✅ MAN page indexing completed with %d pages", indexedCount)
		} else {
			color.Yellow("💡 No MAN pages indexed - RAG features disabled")
			rs.initialized = false
			rs.saveSystemState()
			return nil
		}
	case <-ctx.Done():
		// Timeout - use whatever was indexed
		indexingCompleted <- true // Signal that we're handling timeout
		indexedCount := rs.indexer.GetIndexedCount()
		if indexedCount > 0 {
			color.Yellow("⏰ RAG indexing timed out after %v", utils.FormatDuration(time.Since(startTime)))
			color.Yellow("💡 Using %d partially indexed pages", indexedCount)
		} else {
			color.Yellow("⏰ RAG indexing timed out - no pages indexed")
			rs.initialized = false
			rs.saveSystemState()
			return nil
		}
	}

	// Get whatever pages were indexed (even if partial)
	pages := rs.getAllIndexedPages()
	if len(pages) == 0 {
		color.Yellow("💡 No MAN pages available for vector indexing")
		rs.initialized = false
		rs.saveSystemState()
		return nil
	}

	// DEBUG: Check what pages we're sending to the vector store
	rs.debugVectorStore(pages)

	color.Blue("🔧 Building vector index with %d pages...", len(pages))
	if err := rs.vectorStore.IndexMANPages(pages); err != nil {
		color.Yellow("⚠️  Vector indexing failed: %v", err)
		// Still mark as initialized to avoid re-indexing
		rs.initialized = true
		rs.saveSystemState()
		return nil
	}

	rs.initialized = true
	duration := time.Since(startTime)

	// NEW: Show completion message without timeout reference
	color.Green("🎉 RAG system initialized in %s!", utils.FormatDuration(duration))
	color.Green("📊 Indexed %d MAN pages, %d vector documents",
		len(pages),
		rs.vectorStore.GetStats()["total_documents"])

	return rs.saveSystemState()
}

// IndexAvailableManPages indexes MAN pages in background (non-blocking)
func (rs *RAGSystem) IndexAvailableManPages() {
	// Only index if we don't have an existing state
	if rs.hasExistingState() {
		color.Blue("💡 RAG system already initialized, skipping background indexing")
		return
	}

	go func() {
		color.Blue("🔄 Background RAG indexing started...")
		if err := rs.Initialize(); err != nil {
			color.Yellow("⚠️  Background indexing completed with issues: %v", err)
		} else {
			if rs.initialized {
				color.Green("✅ Background RAG indexing completed successfully")
			} else {
				color.Yellow("⚠️  Background RAG indexing completed but system not initialized")
			}
		}
	}()
}

// Retrieve retrieves relevant command information for a query
func (rs *RAGSystem) Retrieve(query string) (*RetrievalResult, error) {
	if !rs.initialized {
		return &RetrievalResult{}, nil // Return empty result if not initialized
	}

	color.Blue("🔍 RAG Retrieval for: %s", query)
	startTime := time.Now()

	// Extract potential command names from query
	potentialCommands := rs.extractPotentialCommands(query)

	// Search for relevant commands with better filtering
	relevantCommands, err := rs.vectorStore.GetRelevantCommands(query, 3) // Reduced from 5 to 3
	if err != nil {
		color.Yellow("⚠️  RAG search failed: %v", err)
		return &RetrievalResult{}, nil
	}

	// NEW: Filter out irrelevant commands more aggressively
	var filteredCommands []CommandInfo
	for _, cmd := range relevantCommands {
		if rs.isRelevantCommand(query, cmd) {
			filteredCommands = append(filteredCommands, cmd)
		}
	}

	// Get detailed info for potential exact matches
	var exactMatches []CommandInfo
	for _, cmd := range potentialCommands {
		if info, err := rs.vectorStore.GetCommandInfo(cmd); err == nil {
			exactMatches = append(exactMatches, *info)
		}
	}

	// Combine and deduplicate results
	result := rs.combineResults(exactMatches, filteredCommands)
	result.RetrievalTime = time.Since(startTime)

	color.Green("✅ RAG retrieved %d commands in %s",
		len(result.Commands),
		utils.FormatDuration(result.RetrievalTime))

	return result, nil
}

// NEW: Add this method to filter irrelevant commands
func (rs *RAGSystem) isRelevantCommand(query string, cmd CommandInfo) bool {
	queryLower := strings.ToLower(query)
	cmdLower := strings.ToLower(cmd.Name)

	// Filter out git commands for non-git queries
	if strings.HasPrefix(cmdLower, "git-") && !strings.Contains(queryLower, "git") {
		return false
	}

	// Filter out kubectl commands for non-kubernetes queries
	if strings.HasPrefix(cmdLower, "kubectl") && !strings.Contains(queryLower, "kube") {
		return false
	}

	// Filter out dangerous commands for safe queries
	if (cmdLower == "killall" || cmdLower == "rm") &&
		!strings.Contains(queryLower, "kill") && !strings.Contains(queryLower, "remove") {
		return false
	}

	// Filter out commands with very low relevance
	if cmd.Description == "" || len(cmd.Description) < 10 {
		return false
	}

	return true
}

// RetrievalResult contains the results of a RAG retrieval
type RetrievalResult struct {
	Commands      []CommandInfo `json:"commands"`
	Query         string        `json:"query"`
	RetrievalTime time.Duration `json:"retrieval_time"`
	UsedRAG       bool          `json:"used_rag"`
}

// EnhancePrompt enhances an AI prompt with RAG context
func (rs *RAGSystem) EnhancePrompt(userInput, originalPrompt string) string {
	if !rs.initialized || strings.TrimSpace(userInput) == "" {
		return originalPrompt
	}

	result, err := rs.Retrieve(userInput)
	if err != nil || !result.UsedRAG || len(result.Commands) == 0 {
		return originalPrompt
	}

	color.Cyan("🎯 Enhancing prompt with %d relevant commands", len(result.Commands))

	enhancedPrompt := rs.buildEnhancedPrompt(userInput, originalPrompt, result)
	return enhancedPrompt
}

// buildEnhancedPrompt builds a prompt enhanced with command information
func (rs *RAGSystem) buildEnhancedPrompt(userInput, originalPrompt string, result *RetrievalResult) string {
	var sb strings.Builder

	sb.WriteString("ADDITIONAL CONTEXT FROM SYSTEM MANUAL PAGES:\n")
	sb.WriteString("The following command information is available on this system:\n\n")

	for i, cmd := range result.Commands {
		sb.WriteString(fmt.Sprintf("COMMAND %d: %s\n", i+1, cmd.Name))

		if cmd.Description != "" {
			sb.WriteString(fmt.Sprintf("Description: %s\n", cmd.Description))
		}

		if cmd.Synopsis != "" {
			sb.WriteString(fmt.Sprintf("Usage: %s\n", cmd.Synopsis))
		}

		if len(cmd.Options) > 0 {
			sb.WriteString("Common Options: ")
			if len(cmd.Options) > 5 {
				sb.WriteString(strings.Join(cmd.Options[:5], ", "))
				sb.WriteString("...\n")
			} else {
				sb.WriteString(strings.Join(cmd.Options, ", "))
				sb.WriteString("\n")
			}
		}

		if len(cmd.Examples) > 0 {
			sb.WriteString("Examples: ")
			if len(cmd.Examples) > 2 {
				sb.WriteString(strings.Join(cmd.Examples[:2], " | "))
				sb.WriteString("...\n")
			} else {
				sb.WriteString(strings.Join(cmd.Examples, " | "))
				sb.WriteString("\n")
			}
		}

		sb.WriteString("\n")
	}

	sb.WriteString("ORIGINAL PROMPT:\n")
	sb.WriteString(originalPrompt)

	return sb.String()
}

// ExplainCommand provides detailed explanation of a command using RAG
func (rs *RAGSystem) ExplainCommand(command string) (string, error) {
	if !rs.initialized {
		return "", fmt.Errorf("RAG system not initialized")
	}

	info, err := rs.vectorStore.GetCommandInfo(command)
	if err != nil {
		return "", fmt.Errorf("no information found for command: %s", command)
	}

	var explanation strings.Builder
	explanation.WriteString(fmt.Sprintf("📖 **%s** - Command Explanation\n\n", info.Name))

	if info.Description != "" {
		explanation.WriteString(fmt.Sprintf("**Description**: %s\n\n", info.Description))
	}

	if info.Synopsis != "" {
		explanation.WriteString(fmt.Sprintf("**Usage**: `%s`\n\n", info.Synopsis))
	}

	if len(info.Options) > 0 {
		explanation.WriteString("**Common Options**:\n")
		for _, option := range info.Options {
			explanation.WriteString(fmt.Sprintf("  • %s\n", option))
		}
		explanation.WriteString("\n")
	}

	if len(info.Examples) > 0 {
		explanation.WriteString("**Examples**:\n")
		for i, example := range info.Examples {
			if i >= 3 { // Limit to 3 examples
				break
			}
			explanation.WriteString(fmt.Sprintf("  ```bash\n  %s\n  ```\n", example))
		}
	}

	return explanation.String(), nil
}

// GetCommandSuggestions suggests commands based on user intent
func (rs *RAGSystem) GetCommandSuggestions(userInput string) ([]CommandSuggestion, error) {
	if !rs.initialized {
		return []CommandSuggestion{}, nil
	}

	result, err := rs.Retrieve(userInput)
	if err != nil {
		return []CommandSuggestion{}, err
	}

	var suggestions []CommandSuggestion
	for _, cmd := range result.Commands {
		suggestion := CommandSuggestion{
			Command:     cmd.Name,
			Description: cmd.Description,
			Confidence:  rs.calculateConfidence(userInput, cmd),
		}
		suggestions = append(suggestions, suggestion)
	}

	// Sort by confidence
	for i := 0; i < len(suggestions)-1; i++ {
		for j := i + 1; j < len(suggestions); j++ {
			if suggestions[j].Confidence > suggestions[i].Confidence {
				suggestions[i], suggestions[j] = suggestions[j], suggestions[i]
			}
		}
	}

	return suggestions, nil
}

// CommandSuggestion represents a command suggestion
type CommandSuggestion struct {
	Command     string  `json:"command"`
	Description string  `json:"description"`
	Confidence  float32 `json:"confidence"`
}

// GetSystemStats returns RAG system statistics
func (rs *RAGSystem) GetSystemStats() map[string]interface{} {
	stats := make(map[string]interface{})

	stats["initialized"] = rs.initialized
	stats["indexed_pages"] = rs.indexer.GetIndexedCount()

	if rs.initialized {
		vectorStats := rs.vectorStore.GetStats()
		for k, v := range vectorStats {
			stats[k] = v
		}
	}

	return stats
}

// IsInitialized returns whether the RAG system is ready
func (rs *RAGSystem) IsInitialized() bool {
	return rs.initialized
}

// GetIndexingStatus returns the current indexing status
func (rs *RAGSystem) GetIndexingStatus() string {
	return rs.GetInitializationStatus() // Use the new unified method
}

// IsIndexingComplete checks if the RAG system has finished initializing
func (rs *RAGSystem) IsIndexingComplete() bool {
	return rs.initialized
}

// GetInitializationStatus returns detailed initialization status
func (rs *RAGSystem) GetInitializationStatus() string {
	if rs.initialized {
		return "COMPLETED"
	}

	stats := rs.GetSystemStats()
	if pages, ok := stats["indexed_pages"]; ok {
		if p, ok := pages.(int); ok && p > 0 {
			return fmt.Sprintf("PROCESSING (%d pages)", p)
		}
	}

	return "SCANNING"
}

// ========== PRIVATE HELPER METHODS ==========

// extractPotentialCommands extracts potential command names from user query
func (rs *RAGSystem) extractPotentialCommands(query string) []string {
	var commands []string

	// Common patterns that might indicate command names
	words := strings.Fields(strings.ToLower(query))

	for _, word := range words {
		// Skip common words
		if rs.isCommonWord(word) {
			continue
		}

		// Check if word could be a command (alphanumeric, no special chars except -)
		if rs.looksLikeCommand(word) {
			commands = append(commands, word)
		}
	}

	return commands
}

// isCommonWord checks if a word is too common to be a command
func (rs *RAGSystem) isCommonWord(word string) bool {
	commonWords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true, "but": true,
		"in": true, "on": true, "at": true, "to": true, "for": true, "of": true,
		"with": true, "by": true, "from": true, "up": true, "down": true,
		"how": true, "what": true, "when": true, "where": true, "why": true,
		"list": true, "show": true, "display": true, "find": true, "search": true,
		"get": true, "set": true, "create": true, "delete": true, "remove": true,
		"install": true, "update": true, "upgrade": true,
	}

	return commonWords[word] || len(word) < 2
}

// looksLikeCommand checks if a word looks like a command name
func (rs *RAGSystem) looksLikeCommand(word string) bool {
	// Commands are typically short, alphanumeric with possible hyphens
	if len(word) > 20 || len(word) < 2 {
		return false
	}

	// Check for valid command characters
	for _, char := range word {
		if !((char >= 'a' && char <= 'z') ||
			(char >= '0' && char <= '9') ||
			char == '-' || char == '.') {
			return false
		}
	}

	return true
}

// combineResults combines and deduplicates command results
func (rs *RAGSystem) combineResults(exactMatches, relevant []CommandInfo) *RetrievalResult {
	seen := make(map[string]bool)
	var combined []CommandInfo

	// Add exact matches first (higher priority)
	for _, cmd := range exactMatches {
		if !seen[cmd.Name] {
			seen[cmd.Name] = true
			combined = append(combined, cmd)
		}
	}

	// Add relevant commands
	for _, cmd := range relevant {
		if !seen[cmd.Name] {
			seen[cmd.Name] = true
			combined = append(combined, cmd)
		}
	}

	return &RetrievalResult{
		Commands: combined,
		UsedRAG:  len(combined) > 0,
	}
}

// calculateConfidence calculates how relevant a command is to the user input
func (rs *RAGSystem) calculateConfidence(userInput string, cmd CommandInfo) float32 {
	var confidence float32

	userInput = strings.ToLower(userInput)
	cmdName := strings.ToLower(cmd.Name)
	cmdDesc := strings.ToLower(cmd.Description)

	// Exact command name match
	if strings.Contains(userInput, cmdName) {
		confidence += 0.7
	}

	// Command name appears as separate word
	words := strings.Fields(userInput)
	for _, word := range words {
		if word == cmdName {
			confidence += 0.8
			break
		}
	}

	// Description contains relevant words
	descWords := strings.Fields(cmdDesc)
	queryWords := strings.Fields(userInput)

	matches := 0
	for _, qWord := range queryWords {
		for _, dWord := range descWords {
			if qWord == dWord && len(qWord) > 3 {
				matches++
			}
		}
	}

	confidence += float32(matches) * 0.1

	// Cap at 1.0
	if confidence > 1.0 {
		confidence = 1.0
	}

	return confidence
}

// GetAllIndexedPages returns all indexed MAN pages
func (rs *RAGSystem) getAllIndexedPages() []MANPage {
	if rs.indexer == nil {
		return []MANPage{}
	}

	// Use the proper method to get ALL pages from the indexer
	pages := rs.indexer.GetAllIndexedPages()

	if len(pages) == 0 {
		color.Yellow("⚠️  No pages found via GetAllIndexedPages(), trying search...")
		// Fallback: use search to get many pages
		searchResults := rs.indexer.SearchPages("")
		if len(searchResults) > 0 {
			pages = append(pages, searchResults...)
		}
	}

	color.Cyan("🔍 DEBUG: Retrieved %d pages for vector store", len(pages))

	if len(pages) > 0 {
		color.Green("✅ Sample pages:")
		for i := 0; i < min(5, len(pages)); i++ {
			page := pages[i]
			color.Green("  %d. %s: %s", i+1, page.Name,
				utils.TruncateString(page.Description, 50))
		}

		// NEW: Check if we have essential commands
		essentialCommands := []string{"ls", "find", "grep", "cd", "pwd", "cat", "mkdir", "rm", "cp", "mv"}
		var missingEssentials []string
		pagesMap := make(map[string]bool)
		for _, page := range pages {
			pagesMap[page.Name] = true
		}

		for _, cmd := range essentialCommands {
			if !pagesMap[cmd] {
				missingEssentials = append(missingEssentials, cmd)
			}
		}

		if len(missingEssentials) > 0 {
			color.Yellow("⚠️  Missing essential commands: %v", missingEssentials)
		} else {
			color.Green("✅ All essential commands are present")
		}
	} else {
		color.Red("❌ No pages available for vector indexing!")
	}

	return pages
}

// ensureIndexDir creates the RAG index directory
func (rs *RAGSystem) ensureIndexDir() error {
	return os.MkdirAll(rs.indexDir, 0755)
}

// tryLoadExistingIndex attempts to load existing RAG index
func (rs *RAGSystem) tryLoadExistingIndex() bool {
	// Try to load vector store first
	if err := rs.vectorStore.loadVectorIndex(); err != nil {
		color.Yellow("⚠️  Could not load existing vector index: %v", err)
		return false
	}

	if rs.vectorStore.IsInitialized() {
		color.Green("✅ Loaded existing vector index")
		return true
	}

	return false
}

// loadSystemState loads the system state from disk
func (rs *RAGSystem) loadSystemState() bool {
	data, err := os.ReadFile(rs.stateFile)
	if err != nil {
		return false
	}

	var state SystemState
	if err := json.Unmarshal(data, &state); err != nil {
		return false
	}

	// Validate state
	if state.Version != indexVersion {
		return false
	}

	rs.initialized = state.Initialized

	// Try to load vector store if initialized
	if rs.initialized {
		if err := rs.vectorStore.loadVectorIndex(); err == nil {
			color.Green("✅ Loaded RAG index with %d commands", state.TotalCommands)
			return true
		}
	}

	return false
}

// saveSystemState saves the system state to disk
func (rs *RAGSystem) saveSystemState() error {
	state := SystemState{
		Version:     indexVersion,
		Initialized: rs.initialized,
		IndexedTime: time.Now(),
		TotalPages:  rs.indexer.GetIndexedCount(),
	}

	// Safely extract total_documents from vector store stats
	totalCommands := 0
	if v, ok := rs.vectorStore.GetStats()["total_documents"]; ok {
		switch t := v.(type) {
		case int:
			totalCommands = t
		case int64:
			totalCommands = int(t)
		case float64:
			totalCommands = int(t)
		case string:
			if n, err := strconv.Atoi(t); err == nil {
				totalCommands = n
			}
		}
	}
	state.TotalCommands = totalCommands

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(rs.stateFile, data, 0644)
}

// hasExistingState checks if we have any existing state
func (rs *RAGSystem) hasExistingState() bool {
	// Check state file
	if _, err := os.Stat(rs.stateFile); err == nil {
		return true
	}

	// Check vector index
	stats := rs.vectorStore.GetStats()
	if v, ok := stats["total_documents"]; ok {
		switch t := v.(type) {
		case int:
			return t > 0
		case int64:
			return t > 0
		case float64:
			return int(t) > 0
		case string:
			if n, err := strconv.Atoi(t); err == nil {
				return n > 0
			}
		}
	}

	return false
}

// Cleanup cleans up RAG system resources
func (rs *RAGSystem) Cleanup() {
	color.Blue("🧹 Cleaning up RAG system...")
	// Currently no special cleanup needed
}

// debugVectorStore outputs debug info about the vector store
func (rs *RAGSystem) debugVectorStore(pages []MANPage) {
	color.Cyan("🔍 DEBUG: Checking MAN pages for vector store...")
	color.Cyan("  Total pages: %d", len(pages))

	if len(pages) > 0 {
		color.Cyan("  Sample pages:")
		for i := 0; i < min(5, len(pages)); i++ {
			page := pages[i]
			color.Cyan("    %d. %s: %s", i+1, page.Name,
				utils.TruncateString(page.Description, 50))
		}
	} else {
		color.Red("  ❌ No pages available!")
	}
}
