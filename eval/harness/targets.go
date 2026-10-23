package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/patchd/patchd/engine"
)

// Expected is a target's list of known issues (eval/targets/expected/<repo>.yaml).
type Expected struct {
	Repo     string            `yaml:"repo"`
	Commit   string            `yaml:"commit"`
	Findings []ExpectedFinding `yaml:"findings"`
}

// ExpectedFinding is one known issue and the version expected to catch it.
type ExpectedFinding struct {
	Rule   string `yaml:"rule"`
	File   string `yaml:"file"`
	Line   int    `yaml:"line"`  // the sink call's line
	Scope  string `yaml:"scope"` // "v0.4": same function; "v0.5": crosses functions; "v0.6": needs a model or rule not built yet
	Source string `yaml:"source,omitempty"`
}

// Triage is the verdict on findings nobody listed in advance
// (eval/triage/<version>.yaml).
type Triage struct {
	Version string        `yaml:"version"`
	Entries []TriageEntry `yaml:"entries"`
}

// TriageEntry is one triaged finding.
type TriageEntry struct {
	Repo     string `yaml:"repo"`
	Rule     string `yaml:"rule"`
	File     string `yaml:"file"`
	Line     int    `yaml:"line"`
	Verdict  string `yaml:"verdict"` // true-positive, false-positive, unclear
	Cause    string `yaml:"cause,omitempty"`
	Decision string `yaml:"decision,omitempty"` // fixed, defer v0.5, accept
	Note     string `yaml:"note,omitempty"`
	Fixed    bool   `yaml:"fixed,omitempty"` // the false positive no longer occurs
}

// Verdicts.
const (
	TruePositive  = "true-positive"
	FalsePositive = "false-positive"
	Unclear       = "unclear"
)

// LoadExpected reads an expected-findings file.
func LoadExpected(path string) (Expected, error) {
	var e Expected
	data, err := os.ReadFile(path) //nolint:gosec // a path the caller chose
	if err != nil {
		return e, err
	}
	if err := yaml.Unmarshal(data, &e); err != nil {
		return e, fmt.Errorf("%s: %w", path, err)
	}
	for i, f := range e.Findings {
		if f.Rule == "" || f.File == "" || f.Line <= 0 {
			return e, fmt.Errorf("%s: finding %d needs rule, file and line", path, i+1)
		}
		if _, err := ParseVersion(f.Scope); err != nil {
			return e, fmt.Errorf("%s: finding %d: %w", path, i+1, err)
		}
	}
	return e, nil
}

// LoadTriage reads a triage file; a missing file is an empty triage.
func LoadTriage(path string) (Triage, error) {
	var t Triage
	data, err := os.ReadFile(path) //nolint:gosec // a path the caller chose
	if os.IsNotExist(err) {
		return t, nil
	}
	if err != nil {
		return t, err
	}
	if err := yaml.Unmarshal(data, &t); err != nil {
		return t, fmt.Errorf("%s: %w", path, err)
	}
	for i, e := range t.Entries {
		switch e.Verdict {
		case TruePositive, FalsePositive, Unclear:
		default:
			return t, fmt.Errorf("%s: entry %d: verdict %q (want %s, %s or %s)", path, i+1, e.Verdict, TruePositive, FalsePositive, Unclear)
		}
	}
	return t, nil
}

// ParseVersion reads "v0.4" as 4 (minor versions of v0; v1.2 is 102).
func ParseVersion(v string) (int, error) {
	major, minor, ok := strings.Cut(strings.TrimPrefix(v, "v"), ".")
	ma, err1 := strconv.Atoi(major)
	mi, err2 := strconv.Atoi(minor)
	if !ok || err1 != nil || err2 != nil {
		return 0, fmt.Errorf("version %q: want vMAJOR.MINOR", v)
	}
	return ma*100 + mi, nil
}

// TargetScore is how a scan of one target compares with its expected list.
type TargetScore struct {
	Repo       string
	Version    string            // the version being gated, e.g. v0.4
	Found      []ExpectedFinding // expected and found
	Missed     []ExpectedFinding // in scope for Version and not found: fails the gate
	Later      []ExpectedFinding // scoped to a later version and not found: expected misses
	Early      []ExpectedFinding // scoped to a later version but already found
	Unexpected []Classified      // findings nobody listed
}

// Classified is an unexpected finding with its triage, if any.
type Classified struct {
	Finding engine.Finding
	Triage  *TriageEntry
}

// GatePassed reports the milestone gate: every in-scope issue found and
// every unexpected finding triaged.
func (s TargetScore) GatePassed() bool {
	if len(s.Missed) > 0 {
		return false
	}
	for _, u := range s.Unexpected {
		if u.Triage == nil {
			return false
		}
	}
	return true
}

// ScoreTarget compares findings with a target's expected list. A finding
// matches an expected issue with the same rule, file and sink line.
func ScoreTarget(exp Expected, findings []engine.Finding, triage Triage, version string) (TargetScore, error) {
	gate, err := ParseVersion(version)
	if err != nil {
		return TargetScore{}, err
	}
	s := TargetScore{Repo: exp.Repo, Version: version}
	matched := make([]bool, len(findings))
	for _, e := range exp.Findings {
		i := slices.IndexFunc(findings, func(f engine.Finding) bool {
			return f.RuleID == e.Rule && f.Location.Path == e.File && f.Location.StartLine == e.Line
		})
		scope, _ := ParseVersion(e.Scope)
		switch {
		case i >= 0 && scope <= gate:
			s.Found = append(s.Found, e)
		case i >= 0:
			s.Early = append(s.Early, e)
		case scope <= gate:
			s.Missed = append(s.Missed, e)
		default:
			s.Later = append(s.Later, e)
		}
		if i >= 0 {
			matched[i] = true
		}
	}
	for i, f := range findings {
		if !matched[i] {
			s.Unexpected = append(s.Unexpected, Classified{Finding: f, Triage: triage.lookup(exp.Repo, f)})
		}
	}
	return s, nil
}

// Classify triages every finding of a repo without an expected list (the
// reference repos).
func Classify(repo string, findings []engine.Finding, triage Triage) []Classified {
	out := make([]Classified, 0, len(findings))
	for _, f := range findings {
		out = append(out, Classified{Finding: f, Triage: triage.lookup(repo, f)})
	}
	return out
}

func (t Triage) lookup(repo string, f engine.Finding) *TriageEntry {
	for i, e := range t.Entries {
		if e.Repo == repo && e.Rule == f.RuleID && e.File == f.Location.Path && e.Line == f.Location.StartLine {
			return &t.Entries[i]
		}
	}
	return nil
}

// Precision counts verdicts per rule over classified findings.
type Precision struct {
	Rule                   string
	TP, FP, Unclear, Untri int
}

// Value is TP / (TP + FP), or -1 when there are no verdicts.
func (p Precision) Value() float64 {
	if p.TP+p.FP == 0 {
		return -1
	}
	return float64(p.TP) / float64(p.TP+p.FP)
}

// PrecisionByRule tallies classified findings per rule, for the given rules
// (rules without findings are listed with zero counts).
func PrecisionByRule(rules []string, cs []Classified) []Precision {
	idx := map[string]*Precision{}
	var out []Precision
	for _, r := range rules {
		out = append(out, Precision{Rule: r})
	}
	for i := range out {
		idx[out[i].Rule] = &out[i]
	}
	for _, c := range cs {
		p := idx[c.Finding.RuleID]
		if p == nil {
			continue
		}
		switch {
		case c.Triage == nil:
			p.Untri++
		case c.Triage.Verdict == TruePositive:
			p.TP++
		case c.Triage.Verdict == FalsePositive:
			p.FP++
		default:
			p.Unclear++
		}
	}
	return out
}

// ExpectedFiles lists eval/targets/expected/*.yaml under root.
func ExpectedFiles(root string) ([]string, error) {
	return filepath.Glob(filepath.Join(root, "eval", "targets", "expected", "*.yaml"))
}
