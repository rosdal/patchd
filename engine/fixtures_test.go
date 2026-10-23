package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/patchd/patchd/engine/tsbridge"
)

// The fixture suite is the engine's main test: tiny TypeScript and
// JavaScript files under testdata/fixtures/<rule>/{positive,negative} that
// state their expected findings in comments on the sink line.
//
//	exec(`ping ${host}`); // expect: ts/cmdi from=7
//	exec(`echo ${count}`); // expect-none
//
// expect: a finding of that rule must start on this line (and its trace
// must start on line `from` when given). expect-none: no finding may be
// reported on this line. A finding on a line without an expect annotation
// fails the test, so surprise false positives can't slip in. Positive
// files must expect at least one finding; negative files none.
//
// The fixtures share one project (testdata/fixtures/tsconfig.json) and
// types installed by `pnpm fixtures:install`.

var (
	expectRe     = regexp.MustCompile(`//\s*expect:\s*(\S+)(?:\s+from=(\d+))?`)
	expectNoneRe = regexp.MustCompile(`//\s*expect-none\b`)
)

const fixturesDir = "testdata/fixtures"

// fixtureRules maps a fixture folder to the rule it tests.
var fixtureRules = map[string]string{"cmdi": "ts/cmdi", "path-traversal": "ts/path-traversal", "sqli": "ts/sqli"}

type expectation struct {
	file, rule string
	line, from int
	none       bool
}

func TestFixtures(t *testing.T) {
	if _, err := os.Stat(filepath.Join(fixturesDir, "node_modules", "@types", "express")); err != nil {
		t.Fatalf("fixture types are not installed: run `pnpm fixtures:install` (%v)", err)
	}
	res, err := Analyze(context.Background(), fixturesDir, Options{Discover: tsbridge.DiscoverOptions{NoRepoConfig: true}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.Projects != 1 || len(res.Errors) > 0 {
		t.Fatalf("fixtures must load as one project: %d projects, errors %v", res.Stats.Projects, res.Errors)
	}
	exps, counts := readExpectations(t)

	byLine := map[string][]Finding{} // "file:line"
	for _, f := range res.Findings {
		k := f.Location.Path + ":" + strconv.Itoa(f.Location.StartLine)
		byLine[k] = append(byLine[k], f)
	}
	annotated := map[string]bool{}
	for _, e := range exps {
		k := e.file + ":" + strconv.Itoa(e.line)
		annotated[k] = true
		got := byLine[k]
		if e.none {
			for _, f := range got {
				t.Errorf("%s: expect-none, but found %s\n%s", k, f.RuleID, describe(f))
			}
			continue
		}
		i := slices.IndexFunc(got, func(f Finding) bool { return f.RuleID == e.rule })
		switch {
		case i < 0:
			t.Errorf("%s: expected %s, found %s", k, e.rule, ruleIDs(got))
		case e.from != 0 && got[i].Trace[0].Location.StartLine != e.from:
			t.Errorf("%s: expected %s from line %d, trace starts on line %d\n%s", k, e.rule, e.from, got[i].Trace[0].Location.StartLine, describe(got[i]))
		}
	}
	for k, fs := range byLine {
		if !annotated[k] && strings.Contains(k, "/") {
			for _, f := range fs {
				t.Errorf("%s: unexpected %s (add an expect annotation or fix the false positive)\n%s", k, f.RuleID, describe(f))
			}
		}
	}
	for dir, c := range counts {
		t.Logf("%s: %d positive cases, %d negative cases", dir, c[0], c[1])
	}
}

// readExpectations parses every fixture's annotations and checks the
// positive/negative split. It returns cases per rule folder.
func readExpectations(t *testing.T) ([]expectation, map[string][2]int) {
	t.Helper()
	var exps []expectation
	counts := map[string][2]int{}
	for dir, rule := range fixtureRules {
		for _, kind := range []string{"positive", "negative"} {
			files, err := filepath.Glob(filepath.Join(fixturesDir, dir, kind, "*.[tj]s"))
			if err != nil {
				t.Fatal(err)
			}
			if len(files) == 0 {
				t.Errorf("%s/%s: no fixtures", dir, kind)
			}
			for _, path := range files {
				data, err := os.ReadFile(path) //nolint:gosec // testdata
				if err != nil {
					t.Fatal(err)
				}
				rel, err := filepath.Rel(fixturesDir, path)
				if err != nil {
					t.Fatal(err)
				}
				rel = filepath.ToSlash(rel)
				expects := 0
				for i, line := range strings.Split(string(data), "\n") {
					if m := expectRe.FindStringSubmatch(line); m != nil {
						from, _ := strconv.Atoi(m[2])
						exps = append(exps, expectation{file: rel, rule: m[1], line: i + 1, from: from})
						expects++
						if m[1] != rule {
							t.Errorf("%s:%d: expects %s in the %s folder", rel, i+1, m[1], dir)
						}
						c := counts[dir]
						c[0]++
						counts[dir] = c
					} else if expectNoneRe.MatchString(line) {
						exps = append(exps, expectation{file: rel, line: i + 1, none: true})
						if kind == "negative" {
							c := counts[dir]
							c[1]++
							counts[dir] = c
						}
					}
				}
				if kind == "positive" && expects == 0 {
					t.Errorf("%s: a positive fixture without expect annotations", rel)
				}
				if kind == "negative" && expects > 0 {
					t.Errorf("%s: a negative fixture expects findings", rel)
				}
			}
		}
	}
	return exps, counts
}

func ruleIDs(fs []Finding) string {
	if len(fs) == 0 {
		return "nothing"
	}
	var ids []string
	for _, f := range fs {
		ids = append(ids, f.RuleID)
	}
	return strings.Join(ids, ", ")
}

// describe prints a finding's message and trace for a failure report.
func describe(f Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "    %s\n", f.Message)
	for i, s := range f.Trace {
		fmt.Fprintf(&b, "    %d. %d:%d %s | %s\n", i+1, s.Location.StartLine, s.Location.StartColumn, s.Note, s.Snippet)
	}
	return b.String()
}

// TestFixtureCoverage holds the suite to the v0.4 goal: 30 or more positive
// and negative cases per rule, and 100 or more safe cases in all.
func TestFixtureCoverage(t *testing.T) {
	_, counts := readExpectations(t)
	safe := 0
	for dir := range fixtureRules {
		c := counts[dir]
		if c[0] < 30 || c[1] < 30 {
			t.Errorf("%s: %d positive and %d negative cases, want 30 or more of each", dir, c[0], c[1])
		}
		safe += c[1]
	}
	if safe < 100 {
		t.Errorf("%d safe cases, want 100 or more", safe)
	}
}
