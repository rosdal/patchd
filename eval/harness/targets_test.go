package harness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/patchd/patchd/engine"
)

func finding(rule, file string, line int) engine.Finding {
	return engine.Finding{RuleID: rule, Location: engine.Location{Path: file, StartLine: line}}
}

func TestScoreTarget(t *testing.T) {
	exp := Expected{Repo: "app", Findings: []ExpectedFinding{
		{Rule: "ts/cmdi", File: "a.js", Line: 3, Scope: "v0.4"},
		{Rule: "ts/sqli", File: "b.js", Line: 9, Scope: "v0.4"},
		{Rule: "ts/sqli", File: "c.js", Line: 1, Scope: "v0.5"},
		{Rule: "ts/sqli", File: "d.js", Line: 2, Scope: "v0.6"},
	}}
	findings := []engine.Finding{
		finding("ts/cmdi", "a.js", 3),
		finding("ts/sqli", "d.js", 2),           // early
		finding("ts/path-traversal", "e.js", 5), // unexpected, triaged
		finding("ts/cmdi", "f.js", 7),           // unexpected, untriaged
	}
	triage := Triage{Entries: []TriageEntry{{Repo: "app", Rule: "ts/path-traversal", File: "e.js", Line: 5, Verdict: FalsePositive}}}
	s, err := ScoreTarget(exp, findings, triage, "v0.4")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Found) != 1 || len(s.Missed) != 1 || len(s.Later) != 1 || len(s.Early) != 1 || len(s.Unexpected) != 2 {
		t.Fatalf("score = %+v", s)
	}
	if s.GatePassed() {
		t.Error("gate passed with a miss and an untriaged finding")
	}
	s.Missed = nil
	s.Unexpected = s.Unexpected[:1]
	if !s.GatePassed() {
		t.Error("gate failed with nothing missed and everything triaged")
	}
}

func TestPrecisionByRule(t *testing.T) {
	tp, fp := TriageEntry{Verdict: TruePositive}, TriageEntry{Verdict: FalsePositive}
	cs := []Classified{
		{Finding: finding("ts/cmdi", "a", 1), Triage: &tp},
		{Finding: finding("ts/cmdi", "a", 2), Triage: &tp},
		{Finding: finding("ts/cmdi", "a", 3), Triage: &fp},
		{Finding: finding("ts/sqli", "a", 4)},
	}
	got := PrecisionByRule([]string{"ts/cmdi", "ts/path-traversal", "ts/sqli"}, cs)
	if got[0].TP != 2 || got[0].FP != 1 || got[0].Value() < 0.66 || got[0].Value() > 0.67 {
		t.Errorf("cmdi = %+v", got[0])
	}
	if got[1].Value() != -1 || got[2].Untri != 1 {
		t.Errorf("others = %+v", got[1:])
	}
}

func TestParseVersion(t *testing.T) {
	for in, want := range map[string]int{"v0.4": 4, "v0.10": 10, "v1.2": 102} {
		if got, err := ParseVersion(in); err != nil || got != want {
			t.Errorf("ParseVersion(%q) = %d, %v", in, got, err)
		}
	}
	if _, err := ParseVersion("next"); err == nil {
		t.Error("accepted next")
	}
}

// The checked-in expected and triage files must parse.
func TestRepoFiles(t *testing.T) {
	root := filepath.Join("..", "..")
	files, err := ExpectedFiles(root)
	if err != nil || len(files) < 3 {
		t.Fatalf("expected files: %v %v", files, err)
	}
	for _, f := range files {
		if _, err := LoadExpected(f); err != nil {
			t.Error(err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "eval", "triage", "v0.4.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTriage(filepath.Join(root, "eval", "triage", "v0.4.yaml")); err != nil {
		t.Error(err)
	}
}
