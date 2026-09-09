// internal/rag/knowledge_stamp_test.go
// Purpose: the update timestamp must be written by the function that does the
// updating, not by one of its callers.
//
// A user's /status printed:
//
//	KNOWLEDGE  16000 CVEs · 46687 exploits · 1699 KEV · 858 MITRE · updated never
//
// `knowledge_last_update` was set by KnowledgeBootstrap, which is one of
// UpdateAll's three callers. The other two — `/knowledge-update` and the
// first-run path — filled the database and left the key empty, so the report
// contradicted its own record count.
package rag

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"
)

// The stamp must live inside UpdateAll.
//
// Asserted at the source level because the alternative is a network fetch: the
// function is internet-gated and returns ErrOffline before touching a single
// source, so a behavioural test of the success path would need the whole
// pipeline. What can be checked cheaply is the property that broke — WHERE the
// fact is recorded — and it is checked against UpdateAll's own body rather
// than the file, so a stamp in some other function cannot satisfy it.
func TestUpdateAllStampsTheTimestampItself(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "updater.go", nil, 0)
	if err != nil {
		t.Fatalf("parse updater.go: %v", err)
	}

	var found bool
	var checked bool
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "UpdateAll" || fn.Body == nil {
			continue
		}
		checked = true
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok || id.Name != "setMeta" {
				return true
			}
			for _, arg := range call.Args {
				if a, ok := arg.(*ast.Ident); ok && a.Name == "metaKnowledgeUpdated" {
					found = true
				}
			}
			return true
		})
	}
	if !checked {
		t.Fatal("UpdateAll not found — the test cannot reach what it checks")
	}
	if !found {
		t.Error("UpdateAll does not stamp metaKnowledgeUpdated. Recording it at one call " +
			"site is what made /status print \"updated never\" beside 64,253 records: the " +
			"other two callers fill the database and omit the fact.")
	}
}

// And it must not be stamped in KnowledgeBootstrap as well.
//
// Two writers of one fact is how they drift — and here the second would be
// redundant rather than wrong, which is exactly the kind of duplicate that
// survives for years.
func TestBootstrapDoesNotAlsoStamp(t *testing.T) {
	src, err := parser.ParseFile(token.NewFileSet(), "bootstrap.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse bootstrap.go: %v", err)
	}
	for _, decl := range src.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "KnowledgeBootstrap" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "setMeta" {
					t.Error("KnowledgeBootstrap stamps the timestamp again — UpdateAll owns " +
						"that now, and one fact with two writers is one fact too many")
				}
			}
			return true
		})
	}
}

// The read path must round-trip what the write path stores, or the report shows
// an empty string for a database that has one.
func TestKnowledgeLastUpdateRoundTrips(t *testing.T) {
	db := newTestDB(t)

	if got := KnowledgeLastUpdate(db); got != "" {
		t.Errorf("a fresh database reported %q, want empty", got)
	}

	stamp := time.Now().UTC().Format(time.RFC3339)
	setMeta(db, metaKnowledgeUpdated, stamp)

	got := KnowledgeLastUpdate(db)
	if got != stamp {
		t.Fatalf("KnowledgeLastUpdate = %q, want %q", got, stamp)
	}
	// RFC3339, because the value is printed raw into a status panel.
	if _, err := time.Parse(time.RFC3339, got); err != nil {
		t.Errorf("stored timestamp %q does not parse as RFC3339: %v", got, err)
	}
	if strings.Contains(got, " ") {
		t.Errorf("timestamp %q contains a space — it is printed inside a one-line KV row", got)
	}
}

// The bootstrap sentinel reads the same key the updater now writes, so a manual
// /knowledge-update must satisfy it. Otherwise a user who updated by hand still
// gets a background bootstrap on the next online start.
func TestBootstrapSkipsWhenTheUpdaterAlreadyStamped(t *testing.T) {
	db := newTestDB(t)
	setMeta(db, metaKnowledgeUpdated, time.Now().UTC().Format(time.RFC3339))

	// No network is touched: the sentinel is read before the online gate, so
	// this returns nil rather than ErrOffline.
	if err := KnowledgeBootstrap(nil, db); err != nil { //nolint:staticcheck // nil ctx is unused on this path
		t.Errorf("KnowledgeBootstrap = %v, want nil — a stamped database has knowledge, "+
			"and re-bootstrapping it would re-download everything", err)
	}
}
