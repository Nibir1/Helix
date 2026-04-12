// internal/ai/planner.go

package ai

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"helix/internal/telemetry"

	"github.com/fatih/color"
)

// ─────────────────────────────────────────────────────────────────────────────
// THESIS TELEMETRY IMPORTS
// ─────────────────────────────────────────────────────────────────────────────
// Telemetry is enabled via HELIX_TELEMETRY=1 environment variable.
// All telemetry data is collected for thesis evaluation purposes.
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
// PLANNER SCHEMA
// ─────────────────────────────────────────────────────────────────────────────

type IntentType string

const (
	IntentChat      IntentType = "chat"
	IntentShell     IntentType = "shell"
	IntentGit       IntentType = "git"
	IntentPackage   IntentType = "package"
	IntentMultiStep IntentType = "multi_step"
)

type Plan struct {
	Intent IntentType `json:"intent"`
	Steps  []PlanStep `json:"steps"`
	Raw    string     `json:"-"`
}

type PlanStep struct {
	Tool    string            `json:"tool"`
	Message string            `json:"message,omitempty"`
	Command string            `json:"command,omitempty"`
	Action  string            `json:"action,omitempty"`
	Args    map[string]string `json:"args,omitempty"`
}

// Raw intermediate representation
type rawPlan struct {
	Intent IntentType    `json:"intent"`
	Steps  []rawPlanStep `json:"steps"`
}

type rawPlanStep struct {
	Tool    string                 `json:"tool"`
	Message string                 `json:"message,omitempty"`
	Command string                 `json:"command,omitempty"`
	Action  string                 `json:"action,omitempty"`
	Args    map[string]interface{} `json:"args,omitempty"`
}

var (
	jsonObjectRegex = regexp.MustCompile(`(?s)\{.*\}`)
)

// ─────────────────────────────────────────────────────────────────────────────
// BuildPlannerPrompt — ULTRA-STRICT Version
// ─────────────────────────────────────────────────────────────────────────────
func BuildPlannerPrompt(userInput string, envDescription string) string {
	return fmt.Sprintf(`
You are Helix's planning module.

### ABSOLUTE OUTPUT RULES (CRITICAL — DO NOT BREAK)

- Output ONLY a SINGLE valid JSON object.
- NO markdown fences. (NO `+"```json"+` or `+"```"+`).
- NO commentary, no explanations, no surrounding text.
- NO backticks anywhere.
- The FIRST character MUST be '{'.
- The LAST character MUST be '}'.
- JSON MUST be 100%% syntactically valid.
- NEVER truncate JSON.
- NEVER output partial fields, partial strings, or unclosed braces/brackets.

If unsure, output the smallest correct JSON plan.

### STRING SAFETY RULES (NEW — REQUIRED)

To ensure valid JSON, YOU MUST follow these rules for ALL strings:

1. **NO SINGLE QUOTES (') ANYWHERE inside JSON strings.**
   - This prevents model truncation and escaping failures.
   - ALL strings MUST use ONLY double quotes.

2. **NO nested quoting inside shell commands.**
   - Shell commands MUST be simple, flat strings.
   - DO NOT use: sed -i '' 's/x/y/'
   - Instead use safe alternatives like:
       perl -pi -e "s/OLD/NEW/g" FILE

3. **NO multiline strings. ALL strings must be single-line.**

4. **NO trailing commas.**

5. **NO escaping inside arguments. Keep everything simple.**

6. **KEEP JSON COMPACT - avoid unnecessary whitespace to prevent truncation.**

### REQUIRED JSON SCHEMA

{
  "intent": "chat" | "shell" | "git" | "package" | "multi_step",
  "steps": [
    {
      "tool": "response" | "shell" | "git" | "package",
      "message": "...", // for response
      "command": "...", // for shell
      "action": "...", // for git/package
      "args": { "key": "value" }
    }
  ]
}

"steps" MUST be a non-empty array.

### RESPONSE TOOL RULES

- Only "message".
- No "command", "action", or "args".

### SHELL TOOL RULES (UPDATED — NO NESTED QUOTES)

- ONLY "command".
- MUST NOT output:
    apt, apt-get, yum, dnf, pacman, zypper,
    brew, pip, pip3, npm, yarn, pnpm, gem, cargo
- NO destructive commands.
- NO single quotes.
- NO nested quotes.
- Commands MUST be simple and flat.
- Prefer:
    perl -pi -e "s/OLD/NEW/g" FILE
  instead of sed with nested quoting.

### PACKAGE TOOL RULES

- tool = "package"
- action = install | update | remove
- args.name MUST be present
- NEVER output shell install commands.
- NEVER include "command".

### GIT TOOL RULES (SAFE + DANGEROUS OPTION C)

SAFE:
- commit → args.message
- tag → args.name (REQUIRED: must be full string like "v1.1.0")
- add → args.paths
- checkout → args.branch
- create-branch → args.branch

DANGEROUS (allowed, agent requires confirmation):
- push → args.remote, args.branch, args.force
- reset-hard → args.target
- clean → args.mode, args.x
- delete-branch → args.branch

FORBIDDEN:
- pull, merge, rebase, cherry-pick, fetch, clone, init, remote add, etc.

### MULTI-STEP RULES

- intent MUST be "multi_step" if 2+ steps exist.
- Steps may mix ANY tools.
- JSON MUST NOT be truncated.
- ALL steps MUST be complete and valid.
- KEEP steps minimal to avoid truncation.

### TRUNCATION PREVENTION RULES (NEW - CRITICAL)

- KEEP JSON COMPACT - minimize whitespace
- If you have many steps, consider combining them
- ALWAYS ensure complete JSON structure
- CHECK that all braces and brackets are closed
- For the current request, here's the expected complete structure:

Example for version update:
{"intent":"multi_step","steps":[{"tool":"shell","command":"perl -pi -e \"s/1.0.0/1.1.0/g\" README.md"},{"tool":"git","action":"add","args":{"paths":["README.md"]}},{"tool":"git","action":"commit","args":{"message":"Update version in README to 1.1.0"}},{"tool":"git","action":"tag","args":{"name":"v1.1.0"}}]}

### FINAL HARD REQUIREMENT

Return ONLY the COMPLETE JSON object.
NO text before.
NO text after.
NO markdown.
NO backticks.
NO truncation.

### CURRENT REQUEST

User Input: %s

Environment: %s

NOW OUTPUT THE COMPLETE JSON:
`, strings.TrimSpace(userInput), strings.TrimSpace(envDescription))
}

// ─────────────────────────────────────────────────────────────────────────────
// ParsePlanFromModelOutput — WITH TELEMETRY
// ─────────────────────────────────────────────────────────────────────────────
// Records telemetry events for thesis evaluation:
// - JSON parsing success/failure
// - Raw output length for debugging
// - Number of steps parsed
// - Intent classification
// - Validation drops and reasons
// ─────────────────────────────────────────────────────────────────────────────

func ParsePlanFromModelOutput(raw string) (*Plan, error) {
	raw = strings.TrimSpace(raw)

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Record raw LLM output for analysis
	// ─────────────────────────────────────────────────────────────────
	tc := telemetry.GetCollector()
	taskID := tc.GetCurrentTaskID()

	// Store raw output length for debugging
	rawOutputLength := len(raw)

	if raw == "" {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Empty output
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"planning",
			"planner",
			"json_valid",
			false,
			map[string]interface{}{
				"error":             "empty planner output",
				"raw_output_length": 0,
			},
		)
		return nil, fmt.Errorf("empty planner output")
	}

	// Strip illegal markdown fences
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```JSON")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	// Extract JSON
	jsonText := raw
	if !strings.HasPrefix(raw, "{") {
		if match := jsonObjectRegex.FindString(raw); match != "" {
			jsonText = match
		}
	}

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Before JSON parsing attempt
	// ─────────────────────────────────────────────────────────────────
	jsonExtracted := len(jsonText)

	// Decode
	var rp rawPlan
	if err := json.Unmarshal([]byte(jsonText), &rp); err != nil {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: JSON parsing failed
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"planning",
			"planner",
			"json_valid",
			false,
			map[string]interface{}{
				"error":                 fmt.Sprintf("json_unmarshal_failed: %v", err),
				"raw_output_length":     rawOutputLength,
				"json_extracted_length": jsonExtracted,
				"first_50_chars": func() string {
					if len(raw) > 50 {
						return raw[:50]
					}
					return raw
				}(),
			},
		)
		return nil, fmt.Errorf("failed to unmarshal planner JSON: %w", err)
	}

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: JSON parsing succeeded
	// ─────────────────────────────────────────────────────────────────
	tc.Record(
		taskID,
		"planning",
		"planner",
		"json_valid",
		true,
		map[string]interface{}{
			"raw_output_length":     rawOutputLength,
			"json_extracted_length": jsonExtracted,
		},
	)

	plan := &Plan{
		Intent: rp.Intent,
		Steps:  make([]PlanStep, 0, len(rp.Steps)),
		Raw:    raw,
	}

	// Convert steps, normalize lists
	for _, rs := range rp.Steps {
		ps := PlanStep{
			Tool:    rs.Tool,
			Message: rs.Message,
			Command: rs.Command,
			Action:  rs.Action,
			Args:    map[string]string{},
		}

		// Normalize args
		for k, v := range rs.Args {
			if v == nil {
				continue
			}

			switch vv := v.(type) {
			case []interface{}: // list → string
				if len(vv) == 1 {
					ps.Args[k] = strings.TrimSpace(fmt.Sprint(vv[0]))
				} else {
					parts := make([]string, 0, len(vv))
					for _, item := range vv {
						parts = append(parts, strings.TrimSpace(fmt.Sprint(item)))
					}
					ps.Args[k] = strings.Join(parts, " ")
				}
			default:
				ps.Args[k] = strings.TrimSpace(fmt.Sprint(v))
			}
		}

		plan.Steps = append(plan.Steps, ps)
	}

	// Normalize & validate
	fixPlan(plan)

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Record intent classification BEFORE validation
	// ─────────────────────────────────────────────────────────────────
	tc.Record(
		taskID,
		"planning",
		"planner",
		"intent_classified",
		true,
		map[string]interface{}{
			"intent":      string(plan.Intent),
			"steps_count": len(plan.Steps),
			"raw_intent":  string(rp.Intent),
		},
	)

	if err := validatePlan(plan); err != nil {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Validation failed
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"planning",
			"planner",
			"validation_passed",
			false,
			map[string]interface{}{
				"error":        err.Error(),
				"intent":       string(plan.Intent),
				"steps_before": len(plan.Steps),
			},
		)
		return nil, err
	}

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Full plan parsed successfully
	// ─────────────────────────────────────────────────────────────────
	tc.Record(
		taskID,
		"planning",
		"planner",
		"plan_parsed_success",
		true,
		map[string]interface{}{
			"intent":         string(plan.Intent),
			"steps_count":    len(plan.Steps),
			"raw_output_len": rawOutputLength,
		},
	)

	return plan, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// NORMALIZATION — WITH TELEMETRY
// ─────────────────────────────────────────────────────────────────────────────

func fixPlan(p *Plan) {
	intent := strings.ToLower(strings.TrimSpace(string(p.Intent)))
	switch intent {
	case "", "chat":
		p.Intent = IntentChat
	case "shell":
		p.Intent = IntentShell
	case "git":
		p.Intent = IntentGit
	case "package":
		p.Intent = IntentPackage
	case "multi_step":
		p.Intent = IntentMultiStep
	default:
		p.Intent = IntentMultiStep
	}

	for i := range p.Steps {
		s := &p.Steps[i]
		s.Tool = strings.ToLower(strings.TrimSpace(s.Tool))
		s.Action = strings.ToLower(strings.TrimSpace(s.Action))
		s.Command = strings.TrimSpace(s.Command)
		s.Message = strings.TrimSpace(s.Message)

		for k, v := range s.Args {
			s.Args[k] = strings.TrimSpace(v)
		}

		// Package name normalization
		if s.Tool == "package" {
			if s.Action == "upgrade" {
				s.Action = "update"
			}
			if alt, ok := s.Args["package_name"]; ok && s.Args["name"] == "" {
				s.Args["name"] = alt
			}
			delete(s.Args, "package_name")
		}
	}

	// Auto-intent correction
	if p.Intent == IntentChat {
		allPkg := true
		for _, s := range p.Steps {
			if s.Tool != "package" {
				allPkg = false
				break
			}
		}
		if allPkg {
			p.Intent = IntentPackage
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// VALIDATION (SAFE + DANGEROUS GIT ACTIONS) — WITH TELEMETRY
// ─────────────────────────────────────────────────────────────────────────────

func validatePlan(p *Plan) error {
	tc := telemetry.GetCollector()
	taskID := tc.GetCurrentTaskID()

	if len(p.Steps) == 0 {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: No steps produced
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"planning",
			"planner",
			"validation_passed",
			false,
			map[string]interface{}{
				"error":       "no_steps_produced",
				"final_valid": false,
			},
		)
		return fmt.Errorf("planner produced no steps")
	}

	validTools := map[string]bool{
		"response": true,
		"shell":    true,
		"git":      true,
		"package":  true,
	}

	var filtered []PlanStep
	droppedSteps := []string{}

	for _, step := range p.Steps {
		if !validTools[step.Tool] {
			color.Yellow("Dropping unknown tool: %s", step.Tool)
			droppedSteps = append(droppedSteps, fmt.Sprintf("unknown_tool:%s", step.Tool))
			continue
		}

		switch step.Tool {
		case "response":
			if step.Message == "" {
				droppedSteps = append(droppedSteps, "empty_response_message")
				continue
			}
			step.Command = ""
			step.Action = ""
			step.Args = map[string]string{}

		case "shell":
			if step.Command == "" {
				droppedSteps = append(droppedSteps, "empty_shell_command")
				continue
			}
			lc := strings.ToLower(step.Command)
			if containsAny(lc, []string{
				"apt ", "apt-get ", "yum ", "dnf ", "pacman ", "zypper ",
				"brew ", "pip ", "pip3 ", "npm ", "yarn ", "pnpm ",
			}) {
				color.Yellow("Dropping package-manager command: %s", step.Command)
				droppedSteps = append(droppedSteps, "package_manager_in_shell")
				continue
			}
			step.Action = ""
			step.Args = map[string]string{}

		case "package":
			if step.Action == "" {
				droppedSteps = append(droppedSteps, "empty_package_action")
				continue
			}
			switch step.Action {
			case "install", "update", "remove":
			default:
				color.Yellow("Dropping unsupported package action: %s", step.Action)
				droppedSteps = append(droppedSteps, fmt.Sprintf("unsupported_package_action:%s", step.Action))
				continue
			}
			name := strings.TrimSpace(step.Args["name"])
			if name == "" {
				droppedSteps = append(droppedSteps, "empty_package_name")
				continue
			}
			step.Command = ""
			step.Args = map[string]string{"name": name}

		case "git":
			if step.Action == "" {
				droppedSteps = append(droppedSteps, "empty_git_action")
				continue
			}

			switch step.Action {
			case "commit", "tag", "add", "checkout", "create-branch":
			case "push", "reset-hard", "clean", "delete-branch":
				// Dangerous actions allowed but will require confirmation
			default:
				color.Yellow("Dropping unsupported git action: %s", step.Action)
				droppedSteps = append(droppedSteps, fmt.Sprintf("unsupported_git_action:%s", step.Action))
				continue
			}

			step.Command = ""
			cleanArgs := map[string]string{}
			for k, v := range step.Args {
				val := strings.TrimSpace(v)
				if val != "" {
					cleanArgs[k] = val
				}
			}
			step.Args = cleanArgs
		}

		filtered = append(filtered, step)
	}

	if len(filtered) == 0 {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: All steps were dropped during validation
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"planning",
			"planner",
			"validation_passed",
			false,
			map[string]interface{}{
				"error":           "no_valid_steps_after_validation",
				"dropped_count":   len(p.Steps),
				"dropped_reasons": droppedSteps,
				"final_valid":     false,
			},
		)
		return fmt.Errorf("no valid steps after validation")
	}

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Validation passed - record drop statistics
	// ─────────────────────────────────────────────────────────────────
	if len(droppedSteps) > 0 {
		tc.Record(
			taskID,
			"planning",
			"planner",
			"steps_dropped",
			true,
			map[string]interface{}{
				"steps_in":        len(p.Steps),
				"steps_out":       len(filtered),
				"dropped_count":   len(droppedSteps),
				"dropped_reasons": droppedSteps,
			},
		)
	}

	// Record successful validation
	tc.Record(
		taskID,
		"planning",
		"planner",
		"validation_passed",
		true,
		map[string]interface{}{
			"steps_validated": len(filtered),
			"steps_dropped":   len(p.Steps) - len(filtered),
			"final_valid":     true,
		},
	)

	p.Steps = filtered
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// HELPERS
// ─────────────────────────────────────────────────────────────────────────────

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
