package metabolism

import (
	"path/filepath"
	"strings"
)

// What a turn consumed (wire v5): the provenance of content its steps brought
// in from outside the user's own work. Metabolism gives a lesson the least
// trusted provenance of its sources, so a lesson learned from a turn that
// read a web page, or a file from outside the user's repositories, is
// quarantined and must clear the stricter nutrient-test bar (Metabolism
// D-038, option (b) of its poisoning suite's taint gap).
//
// Files inside a git working tree count as the user's own, except dependency
// directories (vendor, node_modules, third_party). That is the trade-off the
// user chose: a planted file in a repository they cloned still counts as
// theirs, and in return the lessons from ordinary coding turns are not all
// quarantined.
const (
	ProvRetrievedWeb = "retrieved-web"
	ProvFileContent  = "file-content"
)

// dependencyDirs hold other people's code inside a repository.
var dependencyDirs = map[string]bool{"vendor": true, "node_modules": true, "third_party": true}

// ConsumedBy says what provenance a step brought into the turn ("" for none):
// web retrievals, and file reads and searches outside the user's own
// repositories. Listings and globs bring names, not content, and are left out.
func ConsumedBy(tool, action, path string, ok bool) string {
	if !ok {
		return ""
	}
	switch {
	case tool == "web":
		return ProvRetrievedWeb
	case tool == "file" && (action == "read" || action == "grep") && path != "" && !OwnFile(path):
		return ProvFileContent
	}
	return ""
}

// OwnFile reports whether an absolute path lies in a git working tree,
// outside its dependency directories.
func OwnFile(path string) bool {
	root, ok := repoRoot(path)
	if !ok {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if dependencyDirs[part] || part == ".git" {
			return false
		}
	}
	return true
}
