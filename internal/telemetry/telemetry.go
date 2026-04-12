// internal/telemetry/telemetry.go

// ─────────────────────────────────────────────────────────────────────────────
// THESIS TELEMETRY COLLECTION SYSTEM
// ─────────────────────────────────────────────────────────────────────────────
//
// Purpose:
// This package provides structured telemetry collection for thesis evaluation.
// It captures all critical decision points in the Helix execution pipeline.
//
// Usage:
// export HELIX_TELEMETRY=1
// export HELIX_TELEMETRY_PATH="./telemetry_task_01.json"
// export HELIX_TASK_ID=1
//
// The telemetry collector records events at these phases:
// - "planning": JSON parsing, intent classification, validation
// - "rag": Document retrieval from vector store
// - "safety": Static analysis, risk scoring, sandbox intervention
// - "execution": Command execution, exit codes
//
// INTEGRATION REQUIRED:
// Call telemetry.RecordEvent() at key points in:
// - internal/ai/planner.go (after JSON parsing)
// - internal/rag/ (after document retrieval)
// - internal/commands/shell_safety.go (after risk analysis)
// - internal/agent/agent.go (before exit, call telemetry.Close())
//
// ─────────────────────────────────────────────────────────────────────────────

package telemetry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// TELEMETRY EVENT STRUCTURES
// ─────────────────────────────────────────────────────────────────────────────

// TelemetryEvent represents a single discrete event in the execution pipeline.
// Each event captures the phase, component, and outcome for analysis.
type TelemetryEvent struct {
	// Timestamp records when this event occurred
	Timestamp time.Time `json:"timestamp"`

	// TaskID identifies which evaluation task this belongs to (1-50)
	TaskID int `json:"task_id"`

	// Phase indicates which pipeline phase: "planning", "rag", "safety", "execution"
	Phase string `json:"phase"`

	// Component identifies which subsystem: "planner", "rag_system", "static_analyzer", etc.
	Component string `json:"component"`

	// EventType is the specific event type for this phase
	EventType string `json:"event_type"`

	// Success indicates whether this event represented a successful operation
	Success bool `json:"success"`

	// Data contains flexible key-value pairs specific to this event type
	Data map[string]interface{} `json:"data"`
}

// TelemetryExport is the complete telemetry session export format
type TelemetryExport struct {
	SessionID   string           `json:"session_id"`
	ExportTime  time.Time        `json:"export_time"`
	TaskID      int              `json:"task_id"`
	Environment string           `json:"environment"`
	Events      []TelemetryEvent `json:"events"`
}

// ─────────────────────────────────────────────────────────────────────────────
// TELEMETRY COLLECTOR (SINGLETON)
// ─────────────────────────────────────────────────────────────────────────────

// TelemetryCollector manages all telemetry collection for the evaluation.
// It uses double-checked locking for thread-safe singleton initialization.
type TelemetryCollector struct {
	mu          sync.RWMutex
	events      []TelemetryEvent
	currentTask int
	sessionID   string
	logPath     string
	enabled     bool
	initialized bool
}

var (
	// collector is the singleton telemetry collector instance
	collector *TelemetryCollector

	// once ensures thread-safe singleton initialization
	once sync.Once
)

// GetCollector returns the singleton telemetry collector.
// It is automatically initialized based on environment variables.
//
// Environment Variables:
//
//	HELIX_TELEMETRY=1 - Enable telemetry collection
//	HELIX_TELEMETRY_PATH - Path to save telemetry JSON file
//	HELIX_TASK_ID - Current task ID (1-50)
//	HELIX_ENVIRONMENT - Environment description string
func GetCollector() *TelemetryCollector {
	once.Do(func() {
		collector = &TelemetryCollector{
			events:    make([]TelemetryEvent, 0),
			enabled:   os.Getenv("HELIX_TELEMETRY") == "1",
			logPath:   os.Getenv("HELIX_TELEMETRY_PATH"),
			sessionID: generateSessionID(),
		}

		// Parse task ID from environment
		if taskIDStr := os.Getenv("HELIX_TASK_ID"); taskIDStr != "" {
			if id, err := strconv.Atoi(taskIDStr); err == nil {
				collector.currentTask = id
			}
		}

		collector.initialized = true

		// Auto-save on process exit if enabled
		if collector.enabled {
			// Ensure directory exists early
			if collector.logPath != "" {
				dir := filepath.Dir(collector.logPath)
				if dir != "" && dir != "." {
					os.MkdirAll(dir, 0755)
				}
			}
		}
	})
	return collector
}

// generateSessionID creates a unique identifier for this telemetry session
func generateSessionID() string {
	return fmt.Sprintf("helix_%s_%d",
		time.Now().Format("20060102_150405"),
		os.Getpid())
}

// ─────────────────────────────────────────────────────────────────────────────
// CORE TELEMETRY METHODS
// ─────────────────────────────────────────────────────────────────────────────

// Record records a telemetry event.
// This is the primary method for collecting evaluation data.
//
// Parameters:
//
//	taskID: Evaluation task identifier (1-50)
//	phase: Pipeline phase: "planning", "rag", "safety", "execution"
//	component: Subsystem name: "planner", "rag_system", "static_analyzer", etc.
//	eventType: Specific event type for this subsystem
//	success: Whether this operation succeeded
//	data: Flexible key-value pairs with event-specific details
//
// Example:
//
//	tc.Record(1, "planning", "planner", "json_valid", true, map[string]interface{}{
//	"intent": "shell",
//	"steps_count": 2,
//	})
func (tc *TelemetryCollector) Record(taskID int, phase, component, eventType string, success bool, data map[string]interface{}) {
	// Fast path: if telemetry is disabled, do nothing
	if !tc.enabled {
		return
	}

	// Ensure taskID is set
	if taskID == 0 {
		taskID = tc.currentTask
	}

	// Ensure data map is initialized
	if data == nil {
		data = make(map[string]interface{})
	}

	tc.mu.Lock()
	defer tc.mu.Unlock()

	event := TelemetryEvent{
		Timestamp: time.Now(),
		TaskID:    taskID,
		Phase:     phase,
		Component: component,
		EventType: eventType,
		Success:   success,
		Data:      data,
	}

	tc.events = append(tc.events, event)
}

// GetCurrentTaskID returns the current task ID being processed
func (tc *TelemetryCollector) GetCurrentTaskID() int {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.currentTask
}

// SetCurrentTask sets the current task being processed.
// Call this before starting each evaluation task.
func (tc *TelemetryCollector) SetCurrentTask(taskID int) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.currentTask = taskID
}

// GetEventCount returns the number of events collected so far
func (tc *TelemetryCollector) GetEventCount() int {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return len(tc.events)
}

// IsEnabled returns whether telemetry collection is enabled
func (tc *TelemetryCollector) IsEnabled() bool {
	return tc.enabled
}

// GetLogPath returns the configured log file path
func (tc *TelemetryCollector) GetLogPath() string {
	return tc.logPath
}

// ─────────────────────────────────────────────────────────────────────────────
// EXPORT METHODS
// ─────────────────────────────────────────────────────────────────────────────

// Export exports all collected telemetry as formatted JSON bytes
func (tc *TelemetryCollector) Export() ([]byte, error) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	export := TelemetryExport{
		SessionID:   tc.sessionID,
		ExportTime:  time.Now(),
		TaskID:      tc.currentTask,
		Environment: os.Getenv("HELIX_ENVIRONMENT"),
		Events:      make([]TelemetryEvent, len(tc.events)),
	}

	copy(export.Events, tc.events)

	return json.MarshalIndent(export, "", " ")
}

// ExportMap exports telemetry as a map for flexible processing
func (tc *TelemetryCollector) ExportMap() map[string]interface{} {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	// Summarize events by type
	eventSummary := make(map[string]int)
	successCount := 0
	failureCount := 0

	for _, event := range tc.events {
		key := fmt.Sprintf("%s.%s.%s", event.Phase, event.Component, event.EventType)
		eventSummary[key]++

		if event.Success {
			successCount++
		} else {
			failureCount++
		}
	}

	return map[string]interface{}{
		"session_id":    tc.sessionID,
		"task_id":       tc.currentTask,
		"total_events":  len(tc.events),
		"success_count": successCount,
		"failure_count": failureCount,
		"event_summary": eventSummary,
	}
}

// SaveToFile saves all collected telemetry to the configured log file.
// If no path is configured, this method does nothing.
func (tc *TelemetryCollector) SaveToFile(path string) error {
	if !tc.enabled {
		return nil
	}

	// Use provided path or fall back to configured path
	if path == "" {
		path = tc.logPath
	}

	if path == "" {
		return fmt.Errorf("no telemetry log path configured")
	}

	data, err := tc.Export()
	if err != nil {
		return fmt.Errorf("failed to export telemetry: %w", err)
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create telemetry directory: %w", err)
		}
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write telemetry file: %w", err)
	}

	return nil
}

// SaveToStdout prints telemetry summary to stdout (for debugging)
func (tc *TelemetryCollector) SaveToStdout() {
	if !tc.enabled {
		fmt.Println("[TELEMETRY] Disabled")
		return
	}

	summary := tc.ExportMap()

	fmt.Println("╔══════════════════════════════════════════════════════════════════╗")
	fmt.Println("║ TELEMETRY SUMMARY ║")
	fmt.Println("╠══════════════════════════════════════════════════════════════════╣")
	fmt.Printf("║ Session ID: %s\n", summary["session_id"])
	fmt.Printf("║ Task ID: %d\n", summary["task_id"])
	fmt.Printf("║ Total Events: %d\n", summary["total_events"])
	fmt.Printf("║ Success: %d\n", summary["success_count"])
	fmt.Printf("║ Failures: %d\n", summary["failure_count"])
	fmt.Println("╠══════════════════════════════════════════════════════════════════╣")
	fmt.Println("║ EVENT SUMMARY ║")

	if eventSummary, ok := summary["event_summary"].(map[string]int); ok {
		for key, count := range eventSummary {
			fmt.Printf("║ %-55s %3d\n", key, count)
		}
	}

	fmt.Println("╚══════════════════════════════════════════════════════════════════╝")
}

// Reset clears all collected telemetry.
// Use this between evaluation tasks to avoid data contamination.
func (tc *TelemetryCollector) Reset() {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.events = make([]TelemetryEvent, 0)
	tc.currentTask = 0
}

// Close finalizes telemetry collection and saves to file.
// Call this when shutting down Helix after evaluation.
func (tc *TelemetryCollector) Close() error {
	if tc.enabled && tc.logPath != "" {
		return tc.SaveToFile(tc.logPath)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// HELPER FUNCTIONS
// ─────────────────────────────────────────────────────────────────────────────

// Helper function to check if telemetry is enabled
func IsTelemetryEnabled() bool {
	return os.Getenv("HELIX_TELEMETRY") == "1"
}

// Helper function to get current task ID
func GetCurrentTaskID() int {
	return GetCollector().GetCurrentTaskID()
}

// ─────────────────────────────────────────────────────────────────────────────
// CONVENIENCE PACKAGE-LEVEL FUNCTIONS
// ─────────────────────────────────────────────────────────────────────────────
// These functions provide a simpler API for telemetry recording across
// different packages without needing to explicitly call GetCollector().

// RecordEvent is a convenience wrapper that records a telemetry event using
// the current task ID from the collector. This simplifies calling code in
// other packages.
//
// Parameters:
//
//	phase: Pipeline phase: "planning", "rag", "safety", "execution"
//	component: Subsystem name: "planner", "rag_system", "execute", etc.
//	eventType: Specific event type (e.g., "json_valid", "risk_high")
//	success: Whether this operation succeeded
//	data: Flexible key-value pairs with event-specific details
//
// Example:
//
//	telemetry.RecordEvent("planning", "planner", "json_valid", true, map[string]interface{}{
//	"intent": "shell",
//	"steps_count": 2,
//	})
func RecordEvent(phase, component, eventType string, success bool, data map[string]interface{}) {
	GetCollector().Record(0, phase, component, eventType, success, data)
}

// Record is a convenience wrapper that creates and records a TelemetryEvent.
// It automatically sets the timestamp and uses the current task ID.
//
// Parameters:
//
//	event: A fully populated TelemetryEvent struct
//
// Example:
//
//	telemetry.Record(telemetry.TelemetryEvent{
//	Phase: "safety",
//	Component: "execute",
//	EventType: "command_safe",
//	Success: true,
//	Data: map[string]interface{}{
//	"command": "ls -la",
//	},
//	})
func Record(event TelemetryEvent) {
	// Set timestamp if not already set
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	GetCollector().Record(event.TaskID, event.Phase, event.Component, event.EventType, event.Success, event.Data)
}

// SetTaskID sets the current task ID for all subsequent telemetry events.
// Call this before starting each evaluation task.
//
// Parameters:
//
//	taskID: The evaluation task identifier (1-50)
func SetTaskID(taskID int) {
	GetCollector().SetCurrentTask(taskID)
}

// Flush saves all collected telemetry to the configured file.
// Call this after completing each evaluation task.
//
// Returns:
//
//	error: Any error that occurred while saving, or nil on success
func Flush() error {
	return GetCollector().SaveToFile("")
}

// GetEventCount returns the number of events collected so far
func GetEventCount() int {
	return GetCollector().GetEventCount()
}

// Reset clears all collected telemetry.
// Use this between evaluation tasks to avoid data contamination.
func Reset() {
	GetCollector().Reset()
}

// PrintSummary prints a telemetry summary to stdout.
// Useful for debugging and verification.
func PrintSummary() {
	GetCollector().SaveToStdout()
}

// ─────────────────────────────────────────────────────────────────────────────
// SAFETY INTERVENTION CONSTANTS
// ─────────────────────────────────────────────────────────────────────────────
// These constants standardize safety intervention classification for
// consistent telemetry reporting across all safety modules.

// Safety intervention type constants for standardized classification.
const (
	// InterventionNone indicates no safety intervention was needed
	InterventionNone int = 0

	// InterventionStatic indicates static analysis caught a potential issue
	InterventionStatic int = 1

	// InterventionRisk indicates risk scoring triggered an intervention
	InterventionRisk int = 2

	// InterventionSandbox indicates directory sandboxing was applied
	InterventionSandbox int = 3
)

// Risk level string constants for telemetry reporting.
const (
	// RiskLevelLow indicates low-risk operations
	RiskLevelLow string = "low"

	// RiskLevelMedium indicates medium-risk operations
	RiskLevelMedium string = "medium"

	// RiskLevelHigh indicates high-risk operations
	RiskLevelHigh string = "high"
)
