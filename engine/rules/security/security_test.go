package security

import (
	"strings"
	"testing"

	"github.com/patchd/patchd/engine/models"
	"github.com/patchd/patchd/engine/rules"
)

func TestRegistered(t *testing.T) {
	want := map[string]string{models.RuleCmdi: "CWE-78", models.RulePathTraversal: "CWE-22", models.RuleSQLi: "CWE-89"}
	for id, cwe := range want {
		r, ok := rules.Lookup(id)
		if !ok {
			t.Fatalf("%s not registered", id)
		}
		if r.CWE[0] != cwe || r.Long == "" || r.Fix == "" || r.Short == "" || !strings.Contains(r.Fix, "```") {
			t.Errorf("%s: incomplete metadata %+v", id, r)
		}
	}
	// The built-in sinks name only registered rules.
	if err := models.Builtin().Validate(rules.Exists); err != nil {
		t.Fatal(err)
	}
}

func TestSelect(t *testing.T) {
	got, err := rules.Select([]string{"ts/sqli", " ts/cmdi", "ts/sqli"})
	if err != nil || len(got) != 2 || got[0].ID != "ts/cmdi" || got[1].ID != "ts/sqli" {
		t.Fatalf("Select = %v, %v", got, err)
	}
	if _, err := rules.Select([]string{"ts/nope"}); err == nil || !strings.Contains(err.Error(), "ts/nope") {
		t.Errorf("unknown rule: %v", err)
	}
	def, _ := rules.Select(nil)
	for _, r := range def {
		if r.Disabled {
			t.Errorf("default selection includes disabled rule %s", r.ID)
		}
	}
	if got, err := rules.Select([]string{"ts/path-traversal"}); err != nil || len(got) != 1 {
		t.Errorf("a disabled rule can be selected by ID: %v %v", got, err)
	}
}

func TestMessage(t *testing.T) {
	r, _ := rules.Lookup(models.RuleSQLi)
	got := r.Message("req.query.id", 12, "pool.query", 15, false)
	want := "User input from `req.query.id` (line 12) reaches `pool.query` (line 15) without parameterisation."
	if got != want {
		t.Errorf("Message = %q", got)
	}
	if _, err := rules.ParseSeverity("HIGH"); err != nil {
		t.Error(err)
	}
	if _, err := rules.ParseSeverity("urgent"); err == nil {
		t.Error("ParseSeverity accepted urgent")
	}
}
