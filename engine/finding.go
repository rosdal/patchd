package engine

import "github.com/patchd/patchd/engine/rules"

// Finding is one reported problem. Its shape is the one the whole product
// uses (PR comments, the web app, the trust layer); the JSON form is
// patchd.findings/v1.
type Finding struct {
	RuleID      string      `json:"rule"`
	Severity    Severity    `json:"severity"`
	CWE         []string    `json:"cwe"`
	Message     string      `json:"message"`
	Location    Location    `json:"location"` // the sink call
	Trace       []TraceStep `json:"trace"`    // source → … → sink
	Fingerprint string      `json:"fingerprint"`
	Confidence  Confidence  `json:"confidence"`
	Tags        []string    `json:"tags,omitempty"`
	// OtherSources counts further sources that reach the same sink; the
	// trace shows the most direct one.
	OtherSources int `json:"other_sources,omitempty"`
}

// Severity is how bad a finding is.
type Severity = rules.Severity

// Severities.
const (
	Critical = rules.Critical
	High     = rules.High
	Medium   = rules.Medium
	Low      = rules.Low
)

// Confidence is how sure the engine is that the path exists as reported.
// v0.4 is always High or Medium (Medium when a source matched only through
// the untyped-handler heuristic).
type Confidence string

// Confidences.
const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

// Location is a range in a file. Lines and columns are 1-based; columns
// count UTF-16 code units, as GitHub and SARIF do. Path is relative to the
// scanned root, with forward slashes.
type Location struct {
	Path        string `json:"path"`
	StartLine   int    `json:"start_line"`
	StartColumn int    `json:"start_column"`
	EndLine     int    `json:"end_line"`
	EndColumn   int    `json:"end_column"`
}

// TraceStep is one step of a finding's trace.
type TraceStep struct {
	Location Location `json:"location"`
	Kind     StepKind `json:"kind"`
	Note     string   `json:"note"`    // "read `req.query.id`", "inserted into a template literal"
	Snippet  string   `json:"snippet"` // the source line, trimmed
}

// StepKind is the role of a trace step.
type StepKind string

// Step kinds.
const (
	StepSource    StepKind = "source"
	StepPropagate StepKind = "propagate"
	StepSanitizer StepKind = "sanitizer"
	StepSink      StepKind = "sink"
)

// Finding tags.
const (
	TagIntraprocedural = "intraprocedural"
	TagNumericPruned   = "numeric-pruned"
	TagUntypedHandler  = "untyped-handler"
	TagEnvironment     = "environment"
)
