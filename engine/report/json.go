package report

import (
	"encoding/json"
	"io"

	"github.com/patchd/patchd/engine"
)

// Schema is the version of Patchd's JSON findings format.
const Schema = "patchd.findings/v1"

// Document is the JSON output: the findings with what produced them.
type Document struct {
	Schema   string           `json:"schema"`
	Tool     Tool             `json:"tool"`
	Findings []engine.Finding `json:"findings"`
	Rules    []RuleInfo       `json:"rules"`
	Stats    engine.Stats     `json:"stats"`
	Warnings []string         `json:"warnings,omitempty"`
	Errors   []string         `json:"errors,omitempty"`
}

// Tool names the producer.
type Tool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// RuleInfo is a rule that ran.
type RuleInfo struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	CWE      []string `json:"cwe"`
	Severity string   `json:"severity"`
}

// NewDocument builds the JSON document for a result.
func NewDocument(res *engine.Result, version string) Document {
	doc := Document{
		Schema:   Schema,
		Tool:     Tool{Name: "patchd", Version: version},
		Findings: res.Findings,
		Stats:    res.Stats,
		Warnings: res.Warnings,
	}
	if doc.Findings == nil {
		doc.Findings = []engine.Finding{}
	}
	for _, r := range res.Rules {
		doc.Rules = append(doc.Rules, RuleInfo{ID: r.ID, Name: r.Name, CWE: r.CWE, Severity: string(r.Severity)})
	}
	for _, err := range res.Errors {
		doc.Errors = append(doc.Errors, err.Error())
	}
	return doc
}

// JSON writes the patchd.findings/v1 document.
func JSON(w io.Writer, res *engine.Result, version string) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(NewDocument(res, version))
}
