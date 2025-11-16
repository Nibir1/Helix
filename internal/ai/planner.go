package ai

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/fatih/color"
)

//
// ──────────────────────────────────────────────────────────────
// 📐 PLANNER SCHEMA
// ──────────────────────────────────────────────────────────────
//

// IntentType enumerates the known intents the planner can emit.
type IntentType string

const (
	IntentChat      IntentType = "chat"
	IntentShell     IntentType = "shell"
	IntentGit       IntentType = "git"
	IntentPackage   IntentType = "package" // top-level intent for pure package operations
	IntentMultiStep IntentType = "multi_step"
)

// Plan is the top-level planner output.
type Plan struct {
	Intent IntentType `json:"intent"`
	Steps  []PlanStep `json:"steps"`
	Raw    string     `json:"-"` // For debugging/logging the original text
}

// PlanStep is a single tool invocation.
type PlanStep struct {
	Tool    string            `json:"tool"`              // "response", "shell", "git", "package"
	Message string            `json:"message,omitempty"` // for response
	Command string            `json:"command,omitempty"` // for shell
	Action  string            `json:"action,omitempty"`  // for git & package
	Args    map[string]string `json:"args,omitempty"`    // git/package args (e.g. name/message/branch)
}

// rawPlan/rawPlanStep are used only internally to safely parse
// planner output where args may be non-string (bool, number, etc.).
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
	// Extract the first JSON object from messy model output.
	jsonObjectRegex = regexp.MustCompile(`(?s)\{.*\}`)
)

//
// ──────────────────────────────────────────────────────────────
// 🧠 PLANNER PROMPT (STRICT RULES)
// ──────────────────────────────────────────────────────────────
//

func BuildPlannerPrompt(userInput string, envDescription string) string {
	return fmt.Sprintf(`
You are Helix's planning module. Your job is to output a JSON plan ONLY.

User input:
"%s"

Environment:
%s

Rules:
- Output ONLY valid JSON (no markdown, no comments, no prose)
- Top-level object shape:
  {
    "intent": "chat" | "shell" | "git" | "package" | "multi_step",
    "steps": [
      {
        "tool": "response" | "shell" | "git" | "package",
        "message": "<for response tool>",
        "command": "<for shell tool>",
        "action": "<for git or package tool>",
        "args": { "key": "value", ... }
      }
    ]
  }
- "steps" MUST be a non-empty array.

### RESPONSE RULES
- For tool="response": include ONLY "message".
- Never include "command", "action", or "args".

### SHELL COMMAND RULES
- For tool="shell": include ONLY "command".
- NEVER output package-management shell commands such as:
  apt, apt-get, yum, dnf, pacman, zypper, brew, pip, pip3, npm, yarn, pnpm.
- Helix handles package management internally through the "package" tool.
- Shell steps are for generic CLI commands (ls, sed, find, go, etc.), NOT package managers.

### GIT RULES (SAFE SUBSET ONLY)
- For tool="git": include "action" and "args".
- ALLOWED git actions:
  - "commit"        → args.message
  - "tag"           → args.name
  - "add"           → args.paths   (space-separated or relative paths)
  - "checkout"      → args.branch
  - "create-branch" → args.branch
- DO NOT output other git actions such as:
  "push", "pull", "reset", "clean", "rebase", "merge", "cherry-pick", etc.
- DO NOT output raw shell git commands in tool="git".
- DO NOT output "command" for git steps.

### PACKAGE MANAGEMENT RULES (IMPORTANT)
When the user wants to install, update/upgrade, remove, uninstall, or delete a package,
you MUST use the "package" tool, NOT "shell" and NOT "git".

Package steps look like:
  "tool": "package",
  "action": "install" | "update" | "remove",
  "args": { "name": "<package-name>" }

STRICT RULES:
- ALWAYS treat requests like:
    "install X"
    "update X"
    "upgrade X"
    "remove X"
    "uninstall X"
    "delete X"
  as **package management**, even when X is a CLI tool or dev tool such as:
    git, node, npm, curl, wget, python, go, docker, kubectl, terraform, etc.
- For example:
  - "update git" MUST be a package step, **not** a git "pull".
  - "update node" MUST be a package step.
  - "install curl" MUST be a package step.
- NEVER emit shell commands for package management (no "apt-get install", "brew install", etc.).
- NEVER emit package operations under tool="git".
- ONLY use args.name for the package name. Do NOT use "Name" or "package_name".
- NEVER include a "command" field in a package step.

Examples:

User: "install git"
Output:
{
  "intent": "package",
  "steps": [
    {
      "tool": "package",
      "action": "install",
      "args": { "name": "git" }
    }
  ]
}

User: "remove docker"
Output:
{
  "intent": "package",
  "steps": [
    {
      "tool": "package",
      "action": "remove",
      "args": { "name": "docker" }
    }
  ]
}

User: "update node"
Output:
{
  "intent": "package",
  "steps": [
    {
      "tool": "package",
      "action": "update",
      "args": { "name": "node" }
    }
  ]
}

User: "update git"
Output:
{
  "intent": "package",
  "steps": [
    {
      "tool": "package",
      "action": "update",
      "args": { "name": "git" }
    }
  ]
}

### MULTI-STEP RULES
- intent "multi_step" must include 2 or more steps.
- Steps may mix tools: response, shell, git, package.
- Example: edit file with shell, then commit/tag with git, then send a response.

### FINAL HARD RULE
- DO NOT explain yourself.
- DO NOT wrap the JSON in backticks.
- JSON ONLY.
`, strings.TrimSpace(userInput), strings.TrimSpace(envDescription))
}

//
// ──────────────────────────────────────────────────────────────
// 🧼 PLAN PARSING & VALIDATION
// ──────────────────────────────────────────────────────────────
//

func ParsePlanFromModelOutput(raw string) (*Plan, error) {
	raw = strings.TrimSpace(raw)
	color.Cyan("🔍 Planner raw output: %s", raw)

	if raw == "" {
		return nil, fmt.Errorf("empty planner output")
	}

	// Try to extract first JSON object if there is extra text.
	jsonText := raw
	if !strings.HasPrefix(raw, "{") {
		if match := jsonObjectRegex.FindString(raw); match != "" {
			jsonText = match
		}
	}

	// 1) Unmarshal into tolerant rawPlan (Args = map[string]interface{})
	var rp rawPlan
	if err := json.Unmarshal([]byte(jsonText), &rp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal planner JSON: %w", err)
	}

	// 2) Convert rawPlan → Plan with map[string]string Args
	plan := &Plan{
		Intent: rp.Intent,
		Steps:  make([]PlanStep, 0, len(rp.Steps)),
		Raw:    raw,
	}

	for _, rs := range rp.Steps {
		ps := PlanStep{
			Tool:    rs.Tool,
			Message: rs.Message,
			Command: rs.Command,
			Action:  rs.Action,
			Args:    make(map[string]string),
		}

		for k, v := range rs.Args {
			// Coerce non-string values to string via fmt.Sprint
			if v == nil {
				continue
			}
			ps.Args[k] = strings.TrimSpace(fmt.Sprint(v))
		}

		plan.Steps = append(plan.Steps, ps)
	}

	// 3) Normalize & validate
	fixPlan(plan)

	if err := validatePlan(plan); err != nil {
		return nil, err
	}

	return plan, nil
}

//
// ──────────────────────────────────────────────────────────────
// 🔧 PLAN NORMALIZATION
// ──────────────────────────────────────────────────────────────
//

func fixPlan(p *Plan) {
	// Normalize intent
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
		// Unknown -> multi_step fallback
		p.Intent = IntentMultiStep
	}

	// Normalize steps
	for i := range p.Steps {
		step := &p.Steps[i]

		step.Tool = strings.ToLower(strings.TrimSpace(step.Tool))
		step.Action = strings.ToLower(strings.TrimSpace(step.Action))
		step.Command = strings.TrimSpace(step.Command)
		step.Message = strings.TrimSpace(step.Message)

		// Normalize args map
		if step.Args == nil {
			step.Args = make(map[string]string)
		} else {
			for k, v := range step.Args {
				step.Args[k] = strings.TrimSpace(v)
			}
		}

		// 1) Package action synonyms
		if step.Tool == "package" {
			switch step.Action {
			case "upgrade":
				step.Action = "update"
			case "install", "update", "remove":
				// ok
			default:
				// leave as-is; validator may drop it
			}
		}

		// 2) Clean up known bad arg keys from LLM patterns
		if step.Tool == "package" {
			// Prefer "name" only; drop noisy variants
			if nameAlt, ok := step.Args["package_name"]; ok && step.Args["name"] == "" {
				step.Args["name"] = nameAlt
			}
			if nameAlt, ok := step.Args["Name"]; ok && step.Args["name"] == "" {
				step.Args["name"] = nameAlt
			}
			delete(step.Args, "package_name")
			delete(step.Args, "Name")
		}
	}

	// If intent is empty but all steps are package, set IntentPackage
	if p.Intent == IntentChat { // defaulted earlier
		allPackage := true
		for _, s := range p.Steps {
			if s.Tool != "package" {
				allPackage = false
				break
			}
		}
		if allPackage {
			p.Intent = IntentPackage
		}
	}
}

//
// ──────────────────────────────────────────────────────────────
// 🛡️ PLAN VALIDATION
// ──────────────────────────────────────────────────────────────
//

func validatePlan(p *Plan) error {
	if len(p.Steps) == 0 {
		return fmt.Errorf("planner produced no steps")
	}

	validTools := map[string]bool{
		"response": true,
		"shell":    true,
		"git":      true,
		"package":  true,
	}

	var validSteps []PlanStep

	for _, step := range p.Steps {
		tool := step.Tool

		// Unknown tool → drop
		if !validTools[tool] {
			color.Yellow("⚠️  Dropping step with unknown tool: %s", tool)
			continue
		}

		switch tool {

		case "response":
			if step.Message == "" {
				color.Yellow("⚠️  Dropping response step with empty message")
				continue
			}
			// Response steps must not carry other fields
			step.Command = ""
			step.Action = ""
			step.Args = map[string]string{}

		case "shell":
			if step.Command == "" {
				color.Yellow("⚠️  Dropping shell step with empty command")
				continue
			}
			// Shell steps must not be used for package managers
			lc := strings.ToLower(step.Command)
			if containsAny(lc, []string{
				"apt ", "apt-get ", "yum ", "dnf ", "pacman ", "zypper ",
				"brew ", "pip ", "pip3 ", "npm ", "yarn ", "pnpm ",
			}) {
				color.Yellow("⚠️  Dropping shell step that appears to be package management: %s", step.Command)
				continue
			}
			// Shell step must not carry git/package metadata
			step.Action = ""
			step.Args = map[string]string{}

		case "git":
			if step.Action == "" {
				color.Yellow("⚠️  Dropping git step with empty action")
				continue
			}

			// Only allow safe subset of git actions
			switch step.Action {
			case "commit", "tag", "add", "checkout", "create-branch":
				// ok
			default:
				color.Yellow("⚠️  Dropping git step with unsupported/unsafe action: %s", step.Action)
				continue
			}

			// Git step must not use raw "command"
			step.Command = ""

			// Clean empty args
			cleanArgs := make(map[string]string)
			for k, v := range step.Args {
				if strings.TrimSpace(v) != "" {
					cleanArgs[k] = v
				}
			}
			step.Args = cleanArgs

		case "package":
			if step.Action == "" {
				color.Yellow("⚠️  Dropping package step with empty action")
				continue
			}

			// Only allow install/update/remove for safety
			switch step.Action {
			case "install", "update", "remove":
				// ok
			default:
				color.Yellow("⚠️  Dropping package step with unsupported action: %s", step.Action)
				continue
			}

			name := strings.TrimSpace(step.Args["name"])
			if name == "" {
				color.Yellow("⚠️  Dropping package step missing args.name")
				continue
			}

			// Package steps must not have "command"
			step.Command = ""

			// Strip other noisy args; keep only "name"
			step.Args = map[string]string{
				"name": name,
			}
		}

		validSteps = append(validSteps, step)
	}

	if len(validSteps) == 0 {
		return fmt.Errorf("no valid steps after validation")
	}

	p.Steps = validSteps
	return nil
}

//
// ──────────────────────────────────────────────────────────────
// 🔎 SMALL HELPERS
// ──────────────────────────────────────────────────────────────
//

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
