package models

import (
	"testing"

	"github.com/patchd/patchd/engine/taint"
)

func TestBuiltinValidates(t *testing.T) {
	s := Builtin()
	rules := map[string]bool{RuleCmdi: true, RulePathTraversal: true, RuleSQLi: true}
	if err := s.Validate(func(id string) bool { return rules[id] }); err != nil {
		t.Fatal(err)
	}
	for _, qn := range []string{"node:child_process.exec", "node:fs.readFileSync", "node:fs/promises.readFile", "express:Response.sendFile", "pg:Pool.query"} {
		if len(s.SinksFor(qn)) == 0 {
			t.Errorf("no sink for %s", qn)
		}
	}
	if len(s.SourcesFor("express:Request.query", taint.AccessProperty)) != 1 {
		t.Error("no express query source")
	}
	if len(s.SanitizersFor("global:path.basename")) != 1 {
		t.Error("no basename sanitizer")
	}
}
