package rules

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/patchd/patchd/engine/ir"
	"github.com/patchd/patchd/engine/taint"
	"github.com/patchd/patchd/engine/tsbridge"
)

// Severity is how bad a finding of a rule is by default.
type Severity string

// Severities, most severe first.
const (
	Critical Severity = "critical"
	High     Severity = "high"
	Medium   Severity = "medium"
	Low      Severity = "low"
)

// Rank orders severities: Critical is 4, Low 1, anything else 0.
func (s Severity) Rank() int {
	switch s {
	case Critical:
		return 4
	case High:
		return 3
	case Medium:
		return 2
	case Low:
		return 1
	}
	return 0
}

// ParseSeverity accepts a severity name, case-insensitively.
func ParseSeverity(s string) (Severity, error) {
	sev := Severity(strings.ToLower(s))
	if sev.Rank() == 0 {
		return "", fmt.Errorf("unknown severity %q (want critical, high, medium or low)", s)
	}
	return sev, nil
}

// Rule is a taint rule: it owns a sink kind, its metadata and message, and
// refinements on top of what the taint engine reports.
type Rule struct {
	ID       string   // "ts/sqli"
	Name     string   // "SQL injection"
	CWE      []string // "CWE-89"
	Severity Severity
	Kind     taint.Kind // the sink kind it reports
	Short    string     // one sentence
	Long     string     // Markdown: what it finds and why it matters
	Fix      string     // Markdown: how to fix it, with a safe code example
	// Disabled rules only run when selected by ID (Step 10's precision
	// gate: below 80% true positives a rule ships off by default).
	Disabled bool
	// Advice ends the message: "without parameterisation".
	Advice string
	// Keep refines a candidate; nil keeps every candidate of the kind.
	Keep func(c *Candidate) bool
}

// Candidate is what a rule's Keep sees: one sink argument reached by one
// label, with the function's analysis and the shortest path.
type Candidate struct {
	Prog   *tsbridge.Program
	Result *taint.FuncResult
	Taint  taint.Candidate
	Path   []taint.PathStep
}

// Func returns the candidate's function.
func (c *Candidate) Func() *ir.Func { return c.Result.Func }

// Call returns the sink call.
func (c *Candidate) Call() *ir.Instr { return c.Result.Func.Instr(c.Taint.Call) }

// Args returns the sink call's arguments, receiver excluded.
func (c *Candidate) Args() []ir.ValueID {
	_, args := taint.Layout(c.Func(), c.Call())
	return args
}

// Message is the finding message: "User input from `req.query.id` (line 12)
// reaches `pool.query` (line 15) without parameterisation."
func (r *Rule) Message(source string, sourceLine int, sink string, sinkLine int, environment bool) string {
	origin := "User input"
	if environment {
		origin = "Environment input"
	}
	msg := fmt.Sprintf("%s from `%s` (line %d) reaches `%s` (line %d)", origin, source, sourceLine, sink, sinkLine)
	if r.Advice != "" {
		msg += " " + r.Advice
	}
	return msg + "."
}

var (
	mu       sync.RWMutex
	registry = map[string]*Rule{}
)

// Register adds a rule; rules call it from init. It panics on a duplicate
// or malformed rule, which is a programming error.
func Register(r *Rule) {
	mu.Lock()
	defer mu.Unlock()
	if r.ID == "" || r.Name == "" || len(r.CWE) == 0 || r.Severity.Rank() == 0 || !r.Kind.IsSink() {
		panic(fmt.Sprintf("rules: malformed rule %+v", r))
	}
	if _, ok := registry[r.ID]; ok {
		panic("rules: duplicate rule " + r.ID)
	}
	registry[r.ID] = r
}

// All returns every registered rule, sorted by ID.
func All() []*Rule {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]*Rule, 0, len(registry))
	for _, r := range registry {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b *Rule) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// Lookup returns a rule by ID.
func Lookup(id string) (*Rule, bool) {
	mu.RLock()
	defer mu.RUnlock()
	r, ok := registry[id]
	return r, ok
}

// Exists reports whether a rule is registered: the check taint.Spec's
// Validate wants.
func Exists(id string) bool {
	_, ok := Lookup(id)
	return ok
}

// Select returns the rules named by ids, or every enabled rule when ids is
// empty. Unknown IDs are an error.
func Select(ids []string) ([]*Rule, error) {
	if len(ids) == 0 {
		var out []*Rule
		for _, r := range All() {
			if !r.Disabled {
				out = append(out, r)
			}
		}
		return out, nil
	}
	var out []*Rule
	var unknown []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		r, ok := Lookup(id)
		if !ok {
			unknown = append(unknown, id)
			continue
		}
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("unknown rule(s): %s (patchd rules list shows them)", strings.Join(unknown, ", "))
	}
	slices.SortFunc(out, func(a, b *Rule) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}
