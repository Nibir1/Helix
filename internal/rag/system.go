// internal/rag/system.go
// Package rag provides retrieval-augmented generation components.

package rag

import (
	"context"
	"database/sql"
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

// -----------------------------------------------------------------------------
// Constants & State
// -----------------------------------------------------------------------------

const (
	stateFileName   = "rag_state.json"
	indexVersion    = "1.0"
	maxIndexingTime = 5 * time.Minute
)

type SystemState struct {
	Version       string    `json:"version"`
	Initialized   bool      `json:"initialized"`
	IndexedTime   time.Time `json:"indexed_time"`
	TotalPages    int       `json:"total_pages"`
	TotalCommands int       `json:"total_commands"`
}

// -----------------------------------------------------------------------------
// RAG System
// -----------------------------------------------------------------------------

type RAGSystem struct {
	env            shell.Env
	indexer        *MANIndexer
	vectorStore    *VectorStore
	initialized    bool
	indexDir       string
	stateFile      string
	exploitEntries map[string]ExploitEntry // hardcoded fallback (Phase 3)
	db             *sql.DB                 // SQLite knowledge base (Phase 3.5)
}

// NewSystem constructs a new RAG system.
// The database connection is mandatory for the scalable backend.
func NewSystem(env shell.Env, db *sql.DB) *RAGSystem {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}

	indexDir := filepath.Join(home, ".helix", "rag_index")
	stateFile := filepath.Join(indexDir, stateFileName)

	return &RAGSystem{
		env:            env,
		indexDir:       indexDir,
		stateFile:      stateFile,
		indexer:        NewMANIndexer(env),
		vectorStore:    NewVectorStore(env),
		exploitEntries: make(map[string]ExploitEntry),
		db:             db,
	}
}

// -----------------------------------------------------------------------------
// PUBLIC ENTRY POINT — used by startup + /rag-rebuild
// -----------------------------------------------------------------------------

func (rs *RAGSystem) Initialize() error {
	color.Cyan("RAG System Has Been Initialized...")

	// Make directory
	if err := rs.ensureIndexDir(); err != nil {
		return err
	}

	// 1) Load saved state if possible
	if rs.loadSystemState() {
		color.Green("RAG system loaded from existing state")

		// 2) Load vector index if it exists
	} else if rs.tryLoadExistingIndex() {
		rs.initialized = true
		rs.saveSystemState()
		color.Green("RAG system loaded from existing index")

		// 3) No index → first run → full indexing
	} else {
		if err := rs.fullIndexWithTimeout(); err != nil {
			return err
		}
	}

	// After vector store is ready, ensure MITRE techniques are indexed once
	if rs.initialized && !rs.hasMitreIndex() {
		rs.indexMitre()
	}

	// Ensure exploit entries are loaded into memory (always, for fallback)
	rs.loadExploitEntries()
	if rs.initialized && !rs.hasExploitIndex() {
		rs.indexExploits()
	}

	return nil
}

// Blocks only during startup; same as Initialize() for now.
func (rs *RAGSystem) InitializeBlocking() error {
	return rs.Initialize()
}

// -----------------------------------------------------------------------------
// FULL INDEXING WITH TIMEOUT + PROGRESS BAR
// -----------------------------------------------------------------------------

func (rs *RAGSystem) fullIndexWithTimeout() error {
	color.Blue("Starting MAN page indexing (first time setup)...")

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), maxIndexingTime)
	defer cancel()

	progressTicker := time.NewTicker(1 * time.Second)
	defer progressTicker.Stop()

	indexingDone := make(chan error, 1)
	completedFlag := make(chan struct{}, 1)

	// Estimated total pages for progress bar
	const estimatedTotal = 900

	// MAIN INDEXING GOROUTINE
	go func() {
		err := rs.indexer.IndexAvailableManPages()
		indexingDone <- err
	}()

	// PROGRESS GOROUTINE — only prints one line repeatedly
	go func() {
		for {
			select {
			case <-progressTicker.C:
				if rs.initialized {
					return
				}

				count := rs.indexer.GetIndexedCount()
				elapsed := time.Since(start)
				if elapsed <= 0 {
					elapsed = time.Millisecond
				}

				rate := float64(count) / elapsed.Seconds()
				total := estimatedTotal
				if count > total {
					total = count
				}

				var eta time.Duration
				if rate > 0 && total > count {
					eta = time.Duration(float64(total-count)/rate) * time.Second
				}

				renderProgressBarD(
					"Indexing MAN pages",
					count,
					total,
					rate,
					eta,
				)

			case <-ctx.Done():
				select {
				case <-completedFlag:
					return
				default:
					EndProgressBar()
					color.Yellow("Indexing timeout after %v", utils.FormatDuration(time.Since(start)))
					return
				}
			}
		}
	}()

	// WAIT FOR INDEXING RESULT
	var indexErr error

	select {
	case indexErr = <-indexingDone:
		completedFlag <- struct{}{}
		EndProgressBar()

	case <-ctx.Done():
		completedFlag <- struct{}{}
		EndProgressBar()
		indexErr = fmt.Errorf("indexing timeout")
	}

	// Evaluate result
	count := rs.indexer.GetIndexedCount()
	if count == 0 {
		color.Red("No usable MAN pages indexed")
		rs.initialized = false
		rs.saveSystemState()
		return nil
	}

	if indexErr != nil {
		color.Yellow("Indexing completed with issues: %v", indexErr)
	}

	color.Green("MAN page indexing completed with %d pages", count)

	// Build vector index
	pages := rs.indexer.GetAllIndexedPages()
	if len(pages) == 0 {
		color.Red("No pages available for vector indexing")
		rs.initialized = false
		rs.saveSystemState()
		return nil
	}

	color.Blue("🔧 Building vector index with %d pages...", len(pages))
	if err := rs.vectorStore.IndexMANPages(pages); err != nil {
		color.Yellow("Vector store indexing failed: %v", err)
		rs.initialized = true
		rs.saveSystemState()
		return nil
	}

	rs.initialized = true
	color.Green("RAG fully initialized in %s", utils.FormatDuration(time.Since(start)))
	return rs.saveSystemState()
}

// -----------------------------------------------------------------------------
// RETRIEVAL (MAN pages via vector store)
// -----------------------------------------------------------------------------

func (rs *RAGSystem) Retrieve(query string) ([]CommandInfo, error) {
	if !rs.initialized {
		return nil, nil
	}

	start := time.Now()
	relevant, err := rs.vectorStore.GetRelevantCommands(query, 3)
	if err != nil {
		color.Yellow("RAG search failed: %v", err)
		return nil, nil
	}

	color.Green("RAG retrieved %d commands in %s",
		len(relevant), utils.FormatDuration(time.Since(start)))

	return relevant, nil
}

// -----------------------------------------------------------------------------
// PUBLIC GETTERS — used by /rag-status and others
// -----------------------------------------------------------------------------

func (rs *RAGSystem) GetSystemStats() map[string]interface{} {
	stats := map[string]interface{}{
		"initialized":   rs.initialized,
		"indexed_pages": rs.indexer.GetIndexedCount(),
		"status":        rs.GetInitializationStatus(),
	}

	if rs.initialized {
		for k, v := range rs.vectorStore.GetStats() {
			stats[k] = v
		}
	}

	// Add database stats if available
	if rs.db != nil {
		var cveCount, exploitCount, kevCount, mitreCount int
		rs.db.QueryRow("SELECT COUNT(*) FROM cve").Scan(&cveCount)
		rs.db.QueryRow("SELECT COUNT(*) FROM exploit").Scan(&exploitCount)
		rs.db.QueryRow("SELECT COUNT(*) FROM kev").Scan(&kevCount)
		rs.db.QueryRow("SELECT COUNT(*) FROM mitre_technique").Scan(&mitreCount)
		stats["db_cves"] = cveCount
		stats["db_exploits"] = exploitCount
		stats["db_kev"] = kevCount
		stats["db_mitre"] = mitreCount
	}

	return stats
}

func (rs *RAGSystem) GetInitializationStatus() string {
	if rs.initialized {
		return "COMPLETED"
	}

	count := rs.indexer.GetIndexedCount()
	if count > 0 {
		return fmt.Sprintf("PROCESSING (%d pages)", count)
	}

	return "SCANNING"
}

func (rs *RAGSystem) IsInitialized() bool {
	return rs.initialized
}

// -----------------------------------------------------------------------------
// STATE + INDEX LOADING
// -----------------------------------------------------------------------------

func (rs *RAGSystem) ensureIndexDir() error {
	return os.MkdirAll(rs.indexDir, 0755)
}

func (rs *RAGSystem) tryLoadExistingIndex() bool {
	if err := rs.vectorStore.loadVectorIndex(); err != nil {
		return false
	}

	return rs.vectorStore.IsInitialized()
}

func (rs *RAGSystem) loadSystemState() bool {
	data, err := os.ReadFile(rs.stateFile)
	if err != nil {
		return false
	}

	var state SystemState
	if err := json.Unmarshal(data, &state); err != nil {
		return false
	}

	if state.Version != indexVersion {
		return false
	}

	if state.Initialized {
		if err := rs.vectorStore.loadVectorIndex(); err == nil {
			rs.initialized = true
			return true
		}
	}

	return false
}

func (rs *RAGSystem) saveSystemState() error {
	total := 0
	if v, ok := rs.vectorStore.GetStats()["total_documents"]; ok {
		switch t := v.(type) {
		case int:
			total = t
		case int64:
			total = int(t)
		case float64:
			total = int(t)
		case string:
			if n, err := strconv.Atoi(t); err == nil {
				total = n
			}
		}
	}

	state := SystemState{
		Version:       indexVersion,
		Initialized:   rs.initialized,
		IndexedTime:   time.Now(),
		TotalPages:    rs.indexer.GetIndexedCount(),
		TotalCommands: total,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(rs.stateFile, data, 0644)
}

// -----------------------------------------------------------------------------
// PROGRESS BAR
// -----------------------------------------------------------------------------

var lastProgressLen = 0

func renderProgressBarD(stage string, current, total int, rate float64, eta time.Duration) {
	if total < current {
		total = current
	}
	if total == 0 {
		return
	}

	percent := float64(current) / float64(total)
	if percent > 1 {
		percent = 1
	}

	percentInt := int(percent * 100)
	barWidth := 26
	filled := int(percent * float64(barWidth))

	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
	etaStr := "N/A"
	if eta > 0 {
		etaStr = utils.FormatDuration(eta)
	}

	out := fmt.Sprintf(
		"%s… %d / %d (%d%%) | %.1f/sec | ETA %s  ╢%s╟",
		stage, current, total, percentInt, rate, etaStr, bar,
	)

	if lastProgressLen == 0 {
		fmt.Print("\033[?25l") // hide cursor
	}

	padding := ""
	if lastProgressLen > len(out) {
		padding = strings.Repeat(" ", lastProgressLen-len(out))
	}

	fmt.Printf("\r%s%s", out, padding)
	lastProgressLen = len(out)
}

func EndProgressBar() {
	fmt.Print("\033[?25h\n") // show cursor
	lastProgressLen = 0
}

// -----------------------------------------------------------------------------
// MITRE TECHNIQUES INDEXING (hardcoded fallback)
// -----------------------------------------------------------------------------

func (rs *RAGSystem) hasMitreIndex() bool {
	docs, _ := rs.vectorStore.SearchBySource("T1059", "mitre", 1)
	return len(docs) > 0
}

func (rs *RAGSystem) indexMitre() {
	techniques := loadMitreTechniques()
	if len(techniques) == 0 {
		return
	}
	if err := rs.vectorStore.IndexMitreTechniques(techniques); err != nil {
		color.Yellow("MITRE indexing warning: %v", err)
	} else {
		color.Green("MITRE ATT&CK knowledge base indexed (%d techniques)", len(techniques))
	}
}

func (rs *RAGSystem) RetrieveMitre(query string, max int) ([]MitreTechnique, error) {
	if !rs.initialized {
		return nil, nil
	}
	docs, err := rs.vectorStore.SearchBySource(query, "mitre", max)
	if err != nil {
		return nil, err
	}
	var techniques []MitreTechnique
	seen := map[string]bool{}
	for _, d := range docs {
		id := d.Metadata.Command
		if seen[id] {
			continue
		}
		seen[id] = true
		techniques = append(techniques, MitreTechnique{
			ID:          d.Metadata.Command,
			Name:        d.Metadata.Description,
			Description: d.Content,
		})
	}
	return techniques, nil
}

func (rs *RAGSystem) RetrieveMitreContext(query string, max int) ([]string, error) {
	if !rs.initialized {
		return nil, nil
	}
	docs, err := rs.vectorStore.SearchBySource(query, "mitre", max*2)
	if err != nil {
		return nil, err
	}
	var contexts []string
	seen := map[string]bool{}
	for _, d := range docs {
		if seen[d.Metadata.Command] {
			continue
		}
		seen[d.Metadata.Command] = true
		contexts = append(contexts, d.Content)
		if len(contexts) >= max {
			break
		}
	}
	return contexts, nil
}

// -----------------------------------------------------------------------------
// EXPLOIT ENTRIES INDEXING (hardcoded fallback)
// -----------------------------------------------------------------------------

func (rs *RAGSystem) hasExploitIndex() bool {
	docs, _ := rs.vectorStore.SearchBySource("EDB-", "exploit", 1)
	return len(docs) > 0
}

// loadExploitEntries populates the in-memory exploitEntries map from hardcoded data.
// It should be called on every startup regardless of vector index status.
func (rs *RAGSystem) loadExploitEntries() {
	if len(rs.exploitEntries) > 0 {
		return // already loaded
	}
	entries := loadExploitEntries()
	for _, e := range entries {
		rs.exploitEntries[e.ID] = e
	}
}

// indexExploits now only indexes into the vector store.
func (rs *RAGSystem) indexExploits() {
	entries := loadExploitEntries()
	if len(entries) == 0 {
		return
	}
	if err := rs.vectorStore.IndexExploitEntries(entries); err != nil {
		color.Yellow("Exploit indexing warning: %v", err)
	} else {
		color.Green("Exploit knowledge base indexed (%d entries)", len(entries))
	}
}

func (rs *RAGSystem) GetExploitByID(id string) (ExploitEntry, bool) {
	e, ok := rs.exploitEntries[id]
	return e, ok
}

func (rs *RAGSystem) RetrieveExploitContext(query string, max int) ([]string, error) {
	if !rs.initialized {
		return nil, nil
	}
	docs, err := rs.vectorStore.SearchBySource(query, "exploit", max*2)
	if err != nil {
		return nil, err
	}
	var contexts []string
	seen := map[string]bool{}
	for _, d := range docs {
		if seen[d.Metadata.Command] {
			continue
		}
		seen[d.Metadata.Command] = true
		contexts = append(contexts, d.Content)
		if len(contexts) >= max {
			break
		}
	}
	return contexts, nil
}

func (rs *RAGSystem) GetAllExploitEntries() []ExploitEntry {
	entries := make([]ExploitEntry, 0, len(rs.exploitEntries))
	for _, e := range rs.exploitEntries {
		entries = append(entries, e)
	}
	return entries
}

// -----------------------------------------------------------------------------
// PHASE 3.5 – SQLite-Backed Semantic Search & Update
// -----------------------------------------------------------------------------

// SemanticSearch uses the SQLite database (FTS5 + embeddings) to find relevant
// knowledge entries. Falls back to hardcoded vector store if DB not available.
func (rs *RAGSystem) SemanticSearch(query string, limit int) ([]KnowledgeEntry, error) {
	if rs.db != nil {
		return SemanticSearch(rs.db, query, limit) // from search.go
	}
	// Fallback to old keyword search via vector store (if initialized)
	if !rs.initialized {
		return nil, nil
	}
	// We can approximate by fetching exploit context and MITRE context, but it's not ideal.
	// For now, return empty.
	return nil, nil
}

// UpdateKnowledge triggers a full update of the SQLite knowledge base.
// It assumes rs.db is set.
func (rs *RAGSystem) UpdateKnowledge() error {
	if rs.db == nil {
		return fmt.Errorf("database not initialized")
	}
	return UpdateAll(rs.db)
}

// GetDB returns the underlying database handle (for direct queries if needed).
func (rs *RAGSystem) GetDB() *sql.DB {
	return rs.db
}
