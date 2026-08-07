// internal/rag/system.go
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

type RAGSystem struct {
	env            shell.Env
	indexer        *MANIndexer
	vectorStore    *VectorStore
	initialized    bool
	indexDir       string
	stateFile      string
	exploitEntries map[string]ExploitEntry
	db             *sql.DB
	silent         bool
}

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

func (rs *RAGSystem) SetSilent(s bool) {
	rs.silent = s
	rs.vectorStore.SetSilent(s)
	rs.indexer.SetSilent(s)
}

func (rs *RAGSystem) logCyan(format string, args ...interface{}) {
	if !rs.silent {
		color.Cyan(format, args...)
	}
}
func (rs *RAGSystem) logGreen(format string, args ...interface{}) {
	if !rs.silent {
		color.Green(format, args...)
	}
}
func (rs *RAGSystem) logBlue(format string, args ...interface{}) {
	if !rs.silent {
		color.Blue(format, args...)
	}
}
func (rs *RAGSystem) logYellow(format string, args ...interface{}) {
	if !rs.silent {
		color.Yellow(format, args...)
	}
}
func (rs *RAGSystem) logRed(format string, args ...interface{}) {
	if !rs.silent {
		color.Red(format, args...)
	}
}

func (rs *RAGSystem) Initialize() error {
	rs.logCyan("RAG System Has Been Initialized...")
	if err := rs.ensureIndexDir(); err != nil {
		return err
	}
	if rs.loadSystemState() {
		rs.logGreen("RAG system loaded from existing state")
	} else if rs.tryLoadExistingIndex() {
		rs.initialized = true
		_ = rs.saveSystemState()
		rs.logGreen("RAG system loaded from existing index")
	} else {
		if err := rs.fullIndexWithTimeout(); err != nil {
			return err
		}
	}
	if rs.initialized && !rs.hasMitreIndex() {
		rs.indexMitre()
	}
	rs.loadExploitEntries()
	if rs.initialized && !rs.hasExploitIndex() {
		rs.indexExploits()
	}
	return nil
}

func (rs *RAGSystem) InitializeBlocking() error { return rs.Initialize() }

func (rs *RAGSystem) fullIndexWithTimeout() error {
	rs.logBlue("Starting MAN page indexing (first time setup)...")
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), maxIndexingTime)
	defer cancel()

	progressTicker := time.NewTicker(1 * time.Second)
	defer progressTicker.Stop()

	indexingDone := make(chan error, 1)
	completedFlag := make(chan struct{}, 1)
	const estimatedTotal = 900

	go func() { indexingDone <- rs.indexer.IndexAvailableManPages() }()

	go func() {
		for {
			select {
			case <-progressTicker.C:
				if rs.initialized {
					return
				}
				if rs.silent {
					continue
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
				renderProgressBarD("Indexing MAN pages", count, total, rate, eta)
			case <-ctx.Done():
				select {
				case <-completedFlag:
					return
				default:
					if !rs.silent {
						EndProgressBar()
						rs.logYellow("Indexing timeout after %v", utils.FormatDuration(time.Since(start)))
					}
					return
				}
			}
		}
	}()

	var indexErr error
	select {
	case indexErr = <-indexingDone:
		completedFlag <- struct{}{}
		if !rs.silent {
			EndProgressBar()
		}
	case <-ctx.Done():
		completedFlag <- struct{}{}
		if !rs.silent {
			EndProgressBar()
		}
		indexErr = fmt.Errorf("indexing timeout")
	}

	count := rs.indexer.GetIndexedCount()
	if count == 0 {
		rs.logRed("No usable MAN pages indexed")
		rs.initialized = false
		_ = rs.saveSystemState()
		return nil
	}
	if indexErr != nil {
		rs.logYellow("Indexing completed with issues: %v", indexErr)
	}
	rs.logGreen("MAN page indexing completed with %d pages", count)

	pages := rs.indexer.GetAllIndexedPages()
	if len(pages) == 0 {
		rs.logRed("No pages available for vector indexing")
		rs.initialized = false
		_ = rs.saveSystemState()
		return nil
	}
	rs.logBlue("🔧 Building vector index with %d pages...", len(pages))
	if err := rs.vectorStore.IndexMANPages(pages); err != nil {
		rs.logYellow("Vector store indexing failed: %v", err)
		rs.initialized = true
		_ = rs.saveSystemState()
		return nil
	}
	rs.initialized = true
	rs.logGreen("RAG fully initialized in %s", utils.FormatDuration(time.Since(start)))
	return rs.saveSystemState()
}

func (rs *RAGSystem) Retrieve(query string) ([]CommandInfo, error) {
	if !rs.initialized {
		return nil, nil
	}
	relevant, err := rs.vectorStore.GetRelevantCommands(query, 3)
	if err != nil {
		return nil, nil
	}
	return relevant, nil
}

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
	if rs.db != nil {
		var cveCount, exploitCount, kevCount, mitreCount int
		_ = rs.db.QueryRow("SELECT COUNT(*) FROM cve").Scan(&cveCount)
		_ = rs.db.QueryRow("SELECT COUNT(*) FROM exploit").Scan(&exploitCount)
		_ = rs.db.QueryRow("SELECT COUNT(*) FROM kev").Scan(&kevCount)
		_ = rs.db.QueryRow("SELECT COUNT(*) FROM mitre_technique").Scan(&mitreCount)
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

func (rs *RAGSystem) IsInitialized() bool   { return rs.initialized }
func (rs *RAGSystem) ensureIndexDir() error { return os.MkdirAll(rs.indexDir, 0755) }

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
	out := fmt.Sprintf("%s… %d / %d (%d%%) | %.1f/sec | ETA %s  ╢%s╟", stage, current, total, percentInt, rate, etaStr, bar)
	if lastProgressLen == 0 {
		fmt.Print("\033[?25l")
	}
	padding := ""
	if lastProgressLen > len(out) {
		padding = strings.Repeat(" ", lastProgressLen-len(out))
	}
	fmt.Printf("\r%s%s", out, padding)
	lastProgressLen = len(out)
}

func EndProgressBar() {
	fmt.Print("\033[?25h\n")
	lastProgressLen = 0
}

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
		rs.logYellow("MITRE indexing warning: %v", err)
	} else {
		rs.logGreen("MITRE ATT&CK knowledge base indexed (%d techniques)", len(techniques))
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

func (rs *RAGSystem) hasExploitIndex() bool {
	docs, _ := rs.vectorStore.SearchBySource("EDB-", "exploit", 1)
	return len(docs) > 0
}

func (rs *RAGSystem) loadExploitEntries() {
	if len(rs.exploitEntries) > 0 {
		return
	}
	entries := loadExploitEntries()
	for _, e := range entries {
		rs.exploitEntries[e.ID] = e
	}
}

func (rs *RAGSystem) indexExploits() {
	entries := loadExploitEntries()
	if len(entries) == 0 {
		return
	}
	if err := rs.vectorStore.IndexExploitEntries(entries); err != nil {
		rs.logYellow("Exploit indexing warning: %v", err)
	} else {
		rs.logGreen("Exploit knowledge base indexed (%d entries)", len(entries))
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

func (rs *RAGSystem) SemanticSearch(query string, limit int) ([]KnowledgeEntry, error) {
	if rs.db != nil {
		return SemanticSearch(rs.db, query, limit)
	}
	return nil, nil
}

// FIX: Pass `true` for interactive (user explicitly requested update via slash command).
func (rs *RAGSystem) UpdateKnowledge() error {
	if rs.db == nil {
		return fmt.Errorf("database not initialized")
	}
	return UpdateAll(context.Background(), rs.db, true)
}

func (rs *RAGSystem) GetDB() *sql.DB { return rs.db }

func (rs *RAGSystem) RebuildWithProgress() error {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	for _, dir := range []string{
		rs.indexDir,
		filepath.Join(home, ".helix", "vector_index"),
		filepath.Join(home, ".helix", "man_index"),
	} {
		_ = os.RemoveAll(dir)
	}

	rs.initialized = false
	rs.indexer = NewMANIndexer(rs.env)
	rs.vectorStore = NewVectorStore(rs.env)
	rs.SetSilent(true)

	prog := NewProgress()
	prog.Set("INDEXING MAN PAGES", 0, 900)
	prog.Start()

	poll := make(chan struct{})
	go func() {
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-poll:
				return
			case <-tick.C:
				prog.Set("INDEXING MAN PAGES", rs.indexer.GetIndexedCount(), 900)
			}
		}
	}()

	idxErr := rs.indexer.IndexAvailableManPages()
	close(poll)

	pages := rs.indexer.GetAllIndexedPages()
	if idxErr == nil && len(pages) == 0 {
		prog.Stop()
		return fmt.Errorf("no MAN pages discovered; check MANPATH")
	}

	prog.SetStage("BUILDING VECTOR INDEX")
	vecErr := rs.vectorStore.IndexMANPages(pages)

	prog.SetStage("INDEXING MITRE ATT&CK")
	rs.initialized = true
	if !rs.hasMitreIndex() {
		rs.indexMitre()
	}

	prog.SetStage("INDEXING EXPLOITS")
	rs.loadExploitEntries()
	if !rs.hasExploitIndex() {
		rs.indexExploits()
	}
	_ = rs.saveSystemState()

	prog.Stop()
	if idxErr != nil {
		return idxErr
	}
	return vecErr
}
