// internal/rag/provenance.go
// Purpose: Provenance tiers for retrieved knowledge. Every retrieval result is
// tagged with its source trust tier so the Instruction Firewall can apply
// tier-aware policy (sanitization depth, escalation strictness).
package rag

import "strings"

// Provenance is a trust tier for retrieved content.
type Provenance string

const (
	// ProvMANLocal is local system documentation (most trusted).
	ProvMANLocal Provenance = "man-local"
	// ProvNVD / ProvKEV / ProvMITRE are external threat feeds (semi-trusted).
	ProvNVD   Provenance = "nvd"
	ProvKEV   Provenance = "kev"
	ProvMITRE Provenance = "mitre"
	// ProvExploitRef is exploit reference material (semi-trusted, sensitive).
	ProvExploitRef Provenance = "exploit-ref"
	// ProvUnknown is anything unattributed (untrusted).
	ProvUnknown Provenance = "untrusted"
)

// ProvenanceForSource maps a retrieval source type to its trust tier.
//
// Args:
//   - source: retrieval source tag ("man", "cve", "kev", "mitre", "exploit").
//
// Returns: the Provenance tier. Complexity: O(1).
func ProvenanceForSource(source string) Provenance {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "man", "":
		return ProvMANLocal
	case "cve", "nvd":
		return ProvNVD
	case "kev":
		return ProvKEV
	case "mitre":
		return ProvMITRE
	case "exploit":
		return ProvExploitRef
	default:
		return ProvUnknown
	}
}

// TrustRank orders tiers for policy decisions (higher = more trusted).
//
// Args: none. Returns: int rank. Complexity: O(1).
func (p Provenance) TrustRank() int {
	switch p {
	case ProvMANLocal:
		return 3
	case ProvNVD, ProvKEV, ProvMITRE, ProvExploitRef:
		return 2
	default:
		return 1
	}
}
