// internal/commands/safety/hotpath_bench_test.go
// Purpose: the evidence for the cost of the pipeline's cheapest layer.
//
// These two functions run for every command Helix considers. When the patterns
// were compiled inline at their use sites, the five ordinary commands below
// cost 53µs / 995 allocations to classify and 85µs / 1838 to validate — 199
// allocations per command, spent re-parsing constant strings.
//
// Hoisting the patterns to package level here and in internal/utils (SafeTrim
// and ValidateCommand's hard-block list were doing the same thing) leaves:
//
//	AnalyzeShellRisk            53µs / 995 allocs  ->  15µs / 5
//	ValidateAndCleanShellCommand 85µs / 1838       ->  21µs / 29
//
// Both were re-measured after the utils hoist rather than carried over from the
// intermediate state, where the validator still read 40µs / 684.
//
// Kept as a benchmark rather than a comment so the number can be re-measured
// rather than believed, and so a future inline compile shows up as a cost as
// well as failing TestNoRegexCompiledPerCall.
package safety

import "testing"

// realCommands are ordinary, not adversarial: the point is the price of the
// common case, which is what a hot path is.
var realCommands = []string{
	"ls -la",
	"git status --porcelain",
	"sed -i s/a/b/ file.txt",
	"curl -fsSL https://example.com/get | sudo bash",
	"find . -name '*.go' -newer go.mod -print0 | xargs -0 grep -l TODO",
}

func BenchmarkAnalyzeShellRiskRealCommands(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, c := range realCommands {
			_, _ = AnalyzeShellRisk(c)
		}
	}
}

func BenchmarkValidateAndCleanRealCommands(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, c := range realCommands {
			_, _ = ValidateAndCleanShellCommand(c)
		}
	}
}
