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
	IntentPackage   IntentType = "package" // new top-level intent possible
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
	Args    map[string]string `json:"args,omitempty"`    // git/package args (e.g. name/message)
}

var (
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
- Never output package-management shell commands such as:
  apt, apt-get, yum, brew, dnf, pacman, pip, npm.
- Helix handles package management internally through "package" tool.

### GIT RULES
- For tool="git": include "action" and "args".
  - commit → args.message
  - tag → args.name
  - checkout → args.branch
  - create-branch → args.branch
- DO NOT output raw shell git commands in tool="git".
- DO NOT output "command" for git steps.

### PACKAGE MANAGEMENT RULES
When the user wants to install, update/upgrade, remove, uninstall, or delete a package:
You MUST output a step with:
  "tool": "package",
  "action": "install" | "update" | "remove",
  "args": { "name": "<package-name>" }

STRICT RULES:
- NEVER output "Name" or "package_name". Only args.name is allowed.
- NEVER output shell commands for package management. (e.g., apt-get install git)
- NEVER output extra fields such as "command" for package steps.
- NEVER wrap name in quotes inside args (model must output plain string).
- For multi-step package operations, include multiple "package" steps.

Examples:

User: "install git"
Output:
{
  "tool": "package",
  "action": "install",
  "args": { "name": "git" }
}

User: "remove docker"
Output:
{
  "tool": "package",
  "action": "remove",
  "args": { "name": "docker" }
}

User: "update node"
Output:
{
  "tool": "package",
  "action": "update",
  "args": { "name": "node" }
}

### MULTI-STEP RULES
- multi_step must include 2+ steps.
- Steps may mix tools: response, shell, git, package.

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

	jsonText := raw
	if !strings.HasPrefix(raw, "{") {
		if match := jsonObjectRegex.FindString(raw); match != "" {
			jsonText = match
		}
	}

	var plan Plan
	if err := json.Unmarshal([]byte(jsonText), &plan); err != nil {
		return nil, fmt.Errorf("failed to unmarshal planner JSON: %w", err)
	}
	plan.Raw = raw

	fixPlan(&plan)

	if err := validatePlan(&plan); err != nil {
		return nil, err
	}

	return &plan, nil
}

//
// ──────────────────────────────────────────────────────────────
// 🔧 PLAN NORMALIZATION
// ──────────────────────────────────────────────────────────────
//

func fixPlan(p *Plan) {
	intent := strings.ToLower(string(p.Intent))
	switch intent {
	case "chat", "":
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
		step := &p.Steps[i]

		step.Tool = strings.ToLower(strings.TrimSpace(step.Tool))
		step.Action = strings.ToLower(strings.TrimSpace(step.Action))
		step.Command = strings.TrimSpace(step.Command)
		step.Message = strings.TrimSpace(step.Message)

		if step.Args == nil {
			step.Args = make(map[string]string)
		}

		for k, v := range step.Args {
			step.Args[k] = strings.TrimSpace(v)
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

		// Unknown tool → drop
		if !validTools[step.Tool] {
			color.Yellow("⚠️  Dropping step with unknown tool: %s", step.Tool)
			continue
		}

		switch step.Tool {

		case "response":
			if step.Message == "" {
				color.Yellow("⚠️  Dropping response step with empty message")
				continue
			}
			// Must not have other fields
			step.Command = ""
			step.Action = ""
			step.Args = map[string]string{}

		case "shell":
			if step.Command == "" {
				color.Yellow("⚠️  Dropping shell step with empty command")
				continue
			}
			// Shell must NOT include git/package fields
			step.Action = ""
			step.Args = map[string]string{}

		case "git":
			if step.Action == "" {
				color.Yellow("⚠️  Dropping git step with empty action")
				continue
			}
			// Git must NOT include "command"
			step.Command = ""

		case "package":
			if step.Action == "" {
				color.Yellow("⚠️  Dropping package step with empty action")
				continue
			}

			name := step.Args["name"]
			if name == "" {
				color.Yellow("⚠️  Dropping package step missing args.name")
				continue
			}

			// Package steps must NOT have "command"
			step.Command = ""
		}

		validSteps = append(validSteps, step)
	}

	if len(validSteps) == 0 {
		return fmt.Errorf("no valid steps after validation")
	}

	p.Steps = validSteps
	return nil
}
