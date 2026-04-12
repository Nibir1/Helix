// internal/rag/system.go

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
	"helix/internal/telemetry"
	"helix/internal/utils"

	"github.com/fatih/color"
)

// ─────────────────────────────────────────────────────────────────────────────
// THESIS TELEMETRY INTEGRATION
// ─────────────────────────────────────────────────────────────────────────────
// This file integrates telemetry collection for thesis evaluation.
// Telemetry is enabled via HELIX_TELEMETRY=1 environment variable.
//
// Telemetry events recorded in this file:
//   - RAG initialization success/failure
//   - Document retrieval with scores
//   - Query processing metrics
//   - Index statistics
// ─────────────────────────────────────────────────────────────────────────────

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
	env         shell.Env
	indexer     *MANIndexer
	vectorStore *VectorStore
	initialized bool
	indexDir    string
	stateFile   string
}

// NewSystem constructs a new RAG system
func NewSystem(env shell.Env) *RAGSystem {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}

	indexDir := filepath.Join(home, ".helix", "rag_index")
	stateFile := filepath.Join(indexDir, stateFileName)

	return &RAGSystem{
		env:         env,
		indexDir:    indexDir,
		stateFile:   stateFile,
		indexer:     NewMANIndexer(env),
		vectorStore: NewVectorStore(env),
	}
}

// -----------------------------------------------------------------------------
// PUBLIC ENTRY POINT — used by startup + /rag-rebuild
// -----------------------------------------------------------------------------

// Initialize initializes the RAG system for operation.
// Records telemetry on initialization success or failure.
func (rs *RAGSystem) Initialize() error {
	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Record initialization start
	// ─────────────────────────────────────────────────────────────────
	telemetry.RecordEvent(
		"rag",
		"rag_system",
		"initialization_started",
		true,
		map[string]interface{}{
			"index_dir": rs.indexDir,
		},
	)

	color.Cyan("RAG System Has Been Initialized...")

	// Make directory
	if err := rs.ensureIndexDir(); err != nil {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Directory creation failed
		// ─────────────────────────────────────────────────────────────
		telemetry.RecordEvent(
			"rag",
			"rag_system",
			"initialization_completed",
			false,
			map[string]interface{}{
				"error": fmt.Sprintf("directory_creation_failed: %v", err),
				"phase": "ensure_index_dir",
			},
		)
		return err
	}

	// 1) Load saved state if possible
	if rs.loadSystemState() {
		color.Green("RAG system loaded from existing state")

		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Loaded from saved state
		// ─────────────────────────────────────────────────────────────
		telemetry.RecordEvent(
			"rag",
			"rag_system",
			"initialization_completed",
			true,
			map[string]interface{}{
				"source": "saved_state",
				"pages":  rs.indexer.GetIndexedCount(),
				"phase":  "load_system_state",
			},
		)
		return nil
	}

	// 2) Load vector index if it exists
	if rs.tryLoadExistingIndex() {
		rs.initialized = true
		rs.saveSystemState()
		color.Green("RAG system loaded from existing index")

		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Loaded from existing index
		// ─────────────────────────────────────────────────────────────
		telemetry.RecordEvent(
			"rag",
			"rag_system",
			"initialization_completed",
			true,
			map[string]interface{}{
				"source": "existing_index",
				"pages":  rs.indexer.GetIndexedCount(),
				"phase":  "try_load_existing_index",
			},
		)
		return nil
	}

	// 3) No index → first run → full indexing
	// ─────────────────────────────────────────────────────────────
	// TELEMETRY: Starting full indexing
	// ─────────────────────────────────────────────────────────────
	telemetry.RecordEvent(
		"rag",
		"rag_system",
		"initialization_started",
		true,
		map[string]interface{}{
			"phase": "full_indexing",
		},
	)

	return rs.fullIndexWithTimeout()
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

	// Track indexing errors for telemetry
	var indexingErrors []string

	// MAIN INDEXING GOROUTINE
	go func() {
		err := rs.indexer.IndexAvailableManPages()
		if err != nil {
			indexingErrors = append(indexingErrors, err.Error())
		}
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

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Indexing phase completed
	// ─────────────────────────────────────────────────────────────────
	telemetry.RecordEvent(
		"rag",
		"rag_system",
		"indexing_completed",
		indexErr == nil,
		map[string]interface{}{
			"pages_indexed": count,
			"errors":        indexingErrors,
			"duration_ms":   time.Since(start).Milliseconds(),
			"context":       "man_page_indexing",
		},
	)

	if count == 0 {
		color.Red("No usable MAN pages indexed")
		rs.initialized = false
		rs.saveSystemState()

		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: No pages indexed
		// ─────────────────────────────────────────────────────────────
		telemetry.RecordEvent(
			"rag",
			"rag_system",
			"initialization_completed",
			false,
			map[string]interface{}{
				"error": "no_usable_man_pages_indexed",
				"phase": "man_page_indexing",
			},
		)
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

		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: No pages for vector indexing
		// ─────────────────────────────────────────────────────────────
		telemetry.RecordEvent(
			"rag",
			"rag_system",
			"initialization_completed",
			false,
			map[string]interface{}{
				"error": "no_pages_for_vector_indexing",
				"phase": "vector_index_prep",
			},
		)
		return nil
	}

	color.Blue("🔧 Building vector index with %d pages...", len(pages))

	vectorIndexStart := time.Now()
	if err := rs.vectorStore.IndexMANPages(pages); err != nil {
		color.Yellow("Vector store indexing failed: %v", err)
		rs.initialized = true
		rs.saveSystemState()

		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Vector indexing failed
		// ─────────────────────────────────────────────────────────────
		telemetry.RecordEvent(
			"rag",
			"rag_system",
			"vector_indexing_completed",
			false,
			map[string]interface{}{
				"error":       fmt.Sprintf("vector_indexing_failed: %v", err),
				"pages_count": len(pages),
				"duration_ms": time.Since(vectorIndexStart).Milliseconds(),
			},
		)
		return nil
	}

	rs.initialized = true
	color.Green("RAG fully initialized in %s", utils.FormatDuration(time.Since(start)))

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Full initialization completed successfully
	// ─────────────────────────────────────────────────────────────────
	telemetry.RecordEvent(
		"rag",
		"rag_system",
		"initialization_completed",
		true,
		map[string]interface{}{
			"total_duration_ms": time.Since(start).Milliseconds(),
			"man_pages_indexed": count,
			"vector_documents":  len(pages),
			"index_dir":         rs.indexDir,
		},
	)

	return rs.saveSystemState()
}

// -----------------------------------------------------------------------------
// RETRIEVAL — WITH TELEMETRY
// -----------------------------------------------------------------------------

// Retrieve searches the vector store for relevant command documentation.
// Records telemetry for thesis evaluation including retrieved documents,
// their scores, and query processing metrics.
func (rs *RAGSystem) Retrieve(query string) ([]CommandInfo, error) {
	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Record retrieval start
	// ─────────────────────────────────────────────────────────────────
	retrievalStart := time.Now()

	if !rs.initialized {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: System not initialized
		// ─────────────────────────────────────────────────────────────
		telemetry.RecordEvent(
			"rag",
			"rag_system",
			"retrieve_completed",
			false,
			map[string]interface{}{
				"query":       query,
				"error":       "system_not_initialized",
				"duration_ms": time.Since(retrievalStart).Milliseconds(),
			},
		)
		return nil, nil
	}

	start := time.Now()
	relevant, err := rs.vectorStore.GetRelevantCommands(query, 3)

	// Calculate retrieval duration
	retrievalDuration := time.Since(start)

	if err != nil {
		color.Yellow("RAG search failed: %v", err)

		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Retrieval error
		// ─────────────────────────────────────────────────────────────
		telemetry.RecordEvent(
			"rag",
			"rag_system",
			"retrieve_completed",
			false,
			map[string]interface{}{
				"query":       query,
				"error":       fmt.Sprintf("vector_store_error: %v", err),
				"duration_ms": retrievalDuration.Milliseconds(),
			},
		)
		return nil, nil
	}

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Successful retrieval with document details
	// ─────────────────────────────────────────────────────────────────

	// Extract document names for telemetry
	retrievedDocs := make([]string, 0, len(relevant))
	commandNames := make([]string, 0, len(relevant))

	// Extract source identifiers (command names serve as document identifiers)
	for _, cmd := range relevant {
		retrievedDocs = append(retrievedDocs, cmd.Name)
		commandNames = append(commandNames, cmd.Name)
	}

	// Record successful retrieval
	telemetry.RecordEvent(
		"rag",
		"rag_system",
		"retrieve_completed",
		true,
		map[string]interface{}{
			"query":                 query,
			"documents_retrieved":   retrievedDocs,
			"command_names":         commandNames,
			"retrieval_count":       len(relevant),
			"duration_ms":           retrievalDuration.Milliseconds(),
			"total_rag_duration_ms": time.Since(retrievalStart).Milliseconds(),
		},
	)

	color.Green("RAG retrieved %d commands in %s",
		len(relevant), utils.FormatDuration(time.Since(start)))

	return relevant, nil
}

// -----------------------------------------------------------------------------
// PUBLIC GETTERS — used by /rag-status and others
// -----------------------------------------------------------------------------

// GetSystemStats returns comprehensive RAG system statistics.
// Includes telemetry record of stats retrieval.
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

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Stats retrieval (informational)
	// ─────────────────────────────────────────────────────────────────
	telemetry.RecordEvent(
		"rag",
		"rag_system",
		"stats_retrieved",
		true,
		map[string]interface{}{
			"stats": stats,
		},
	)

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
