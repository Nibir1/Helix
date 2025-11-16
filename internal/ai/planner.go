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

//
// ──────────────────────────────────────────────────────────────
// 🧠 PLANNER PROMPT (UPDATED FOR OPTION C)
// ──────────────────────────────────────────────────────────────
//

func BuildPlannerPrompt(userInput string, envDescription string) string {
	return fmt.Sprintf(`
You are Helix's planning module. Output ONLY valid JSON.

User input:
"%s"

Environment:
%s

Rules:
- Output ONLY JSON
- Follow the exact schema:
  {
    "intent": "chat" | "shell" | "git" | "package" | "multi_step",
    "steps": [...]
  }

### RESPONSE TOOL RULES
- "tool": "response"
- ONLY include "message"

### SHELL TOOL RULES
- "tool": "shell"
- ONLY include "command"
- NEVER output any package manager command (brew, apt, yum, npm, pip, etc.)

### PACKAGE TOOL RULES
- "tool": "package"
- action: install | update | remove
- args.name must be the package name
- NEVER output shell commands for package tasks

### GIT TOOL RULES (NOW INCLUDING OPTION C)
ALLOWED git actions:
  SAFE:
    - "commit"        → args.message
    - "tag"           → args.name
    - "add"           → args.paths
    - "checkout"      → args.branch
    - "create-branch" → args.branch

  DANGEROUS (Option C):
    - "push"          → args.remote, args.branch, args.force
    - "reset-hard"    → args.target
    - "clean"         → args.mode, args.x
    - "delete-branch" → args.branch

NOT ALLOWED:
  pull, merge, rebase, cherry-pick, fetch, clone, etc.

### MULTI-STEP RULES
- Must contain 2+ steps
- Steps can mix tools

### JSON ONLY — NO MARKDOWN, NO TEXT
`, strings.TrimSpace(userInput), strings.TrimSpace(envDescription))
}

//
// ──────────────────────────────────────────────────────────────
// 🧼 PARSE PLAN
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

	var rp rawPlan
	if err := json.Unmarshal([]byte(jsonText), &rp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal planner JSON: %w", err)
	}

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
			Args:    map[string]string{},
		}

		for k, v := range rs.Args {
			if v != nil {
				ps.Args[k] = strings.TrimSpace(fmt.Sprint(v))
			}
		}

		plan.Steps = append(plan.Steps, ps)
	}

	fixPlan(plan)

	if err := validatePlan(plan); err != nil {
		return nil, err
	}

	return plan, nil
}

//
// ──────────────────────────────────────────────────────────────
// 🧽 NORMALIZATION
// ──────────────────────────────────────────────────────────────
//

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

		// package synonyms
		if s.Tool == "package" {
			switch s.Action {
			case "upgrade":
				s.Action = "update"
			}
			if alt, ok := s.Args["package_name"]; ok && s.Args["name"] == "" {
				s.Args["name"] = alt
			}
			if alt, ok := s.Args["Name"]; ok && s.Args["name"] == "" {
				s.Args["name"] = alt
			}
			delete(s.Args, "package_name")
			delete(s.Args, "Name")
		}
	}

	// auto intent
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

//
// ──────────────────────────────────────────────────────────────
// 🛡️ VALIDATION (UPDATED WITH OPTION C)
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

	var out []PlanStep

	for _, step := range p.Steps {
		if !validTools[step.Tool] {
			color.Yellow("⚠️ Dropping step with unknown tool: %s", step.Tool)
			continue
		}

		switch step.Tool {

		// -------------------------------
		// RESPONSE TOOL
		// -------------------------------
		case "response":
			if step.Message == "" {
				color.Yellow("⚠️ Dropping empty response step")
				continue
			}
			step.Command = ""
			step.Action = ""
			step.Args = map[string]string{}

		// -------------------------------
		// SHELL TOOL
		// -------------------------------
		case "shell":
			if step.Command == "" {
				color.Yellow("⚠️ Dropping shell step with empty command")
				continue
			}
			lc := strings.ToLower(step.Command)
			if containsAny(lc, []string{
				"apt ", "apt-get ", "yum ", "dnf ", "pacman ", "zypper ",
				"brew ", "pip ", "pip3 ", "npm ", "yarn ", "pnpm ",
			}) {
				color.Yellow("⚠️ Dropping package-manager shell command: %s", step.Command)
				continue
			}
			step.Action = ""
			step.Args = map[string]string{}

		// -------------------------------
		// PACKAGE TOOL
		// -------------------------------
		case "package":
			if step.Action == "" {
				color.Yellow("⚠️ Dropping package step without action")
				continue
			}
			switch step.Action {
			case "install", "update", "remove":
			default:
				color.Yellow("⚠️ Dropping unsupported package action: %s", step.Action)
				continue
			}

			name := strings.TrimSpace(step.Args["name"])
			if name == "" {
				color.Yellow("⚠️ Dropping package step without name")
				continue
			}
			step.Command = ""
			step.Args = map[string]string{"name": name}

		// -------------------------------
		// GIT TOOL (NOW ALLOWING OPTION C)
		// -------------------------------
		case "git":
			if step.Action == "" {
				color.Yellow("⚠️ Dropping git step without action")
				continue
			}

			switch step.Action {
			// SAFE
			case "commit", "tag", "add", "checkout", "create-branch":
				// ok

			// NEW FOR OPTION C
			case "push", "reset-hard", "clean", "delete-branch":
				// allowed — dangerous actions handled in agent/git.go

			default:
				color.Yellow("⚠️ Dropping unsupported git action: %s", step.Action)
				continue
			}

			step.Command = ""

			// clean args
			clean := map[string]string{}
			for k, v := range step.Args {
				if strings.TrimSpace(v) != "" {
					clean[k] = v
				}
			}
			step.Args = clean
		}

		out = append(out, step)
	}

	if len(out) == 0 {
		return fmt.Errorf("no valid steps after validation")
	}

	p.Steps = out
	return nil
}

//
// ──────────────────────────────────────────────────────────────
// 🔎 HELPERS
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
