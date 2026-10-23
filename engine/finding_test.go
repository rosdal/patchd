package engine

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/patchd/patchd/engine/tsbridge"
)

// scanSource scans one JavaScript file in a fresh directory. The code uses
// require and untyped (req, res) handlers, so it needs no node_modules.
func scanSource(t *testing.T, src string) *Result {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"t"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Analyze(context.Background(), dir, Options{Discover: tsbridge.DiscoverOptions{NoRepoConfig: true}})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func fingerprints(res *Result) []string {
	var out []string
	for _, f := range res.Findings {
		out = append(out, f.Fingerprint)
	}
	slices.Sort(out)
	return out
}

const handlerJS = `const { exec } = require("child_process");
const fs = require("fs");

function handler(req, res) {
  exec("ping " + req.query.host);
  fs.readFileSync(req.params.file);
}

module.exports = { handler, other: (req, res) => exec(req.body.cmd) };
`

func TestFingerprintStability(t *testing.T) {
	base := scanSource(t, handlerJS)
	if len(base.Findings) != 3 {
		t.Fatalf("%d findings, want 3: %+v", len(base.Findings), base.Findings)
	}
	moved := scanSource(t, strings.Repeat("\n", 20)+handlerJS)
	if got, want := fingerprints(moved), fingerprints(base); !slices.Equal(got, want) {
		t.Errorf("moving code 20 lines down changed fingerprints:\n%v\n%v", got, want)
	}
	if moved.Findings[0].Location.StartLine != base.Findings[0].Location.StartLine+20 {
		t.Errorf("locations did not move: %d vs %d", moved.Findings[0].Location.StartLine, base.Findings[0].Location.StartLine)
	}
	reformatted := strings.NewReplacer(
		`exec("ping " + req.query.host);`, "exec(\n    \"ping \"  +  req.query.host\n  );",
		"(req, res) => exec(req.body.cmd)", "(req, res) =>\n  exec( req.body.cmd )",
	).Replace(handlerJS)
	if got, want := fingerprints(scanSource(t, reformatted)), fingerprints(base); !slices.Equal(got, want) {
		t.Errorf("reformatting changed fingerprints:\n%v\n%v", got, want)
	}
	renamed := strings.ReplaceAll(handlerJS, "handler", "serve")
	got := fingerprints(scanSource(t, renamed))
	changed := 0
	for _, fp := range got {
		if !slices.Contains(fingerprints(base), fp) {
			changed++
		}
	}
	if changed != 2 {
		t.Errorf("renaming the function changed %d fingerprints, want the 2 inside it", changed)
	}
}

func TestDeduplicationAndCollisions(t *testing.T) {
	res := scanSource(t, `const { exec } = require("child_process");
function h(req, res) {
  exec(req.query.a + req.body.b);
  exec(req.query.a);
  exec(req.query.a);
}
`)
	if len(res.Findings) != 3 {
		t.Fatalf("%d findings, want 3", len(res.Findings))
	}
	if res.Findings[0].OtherSources != 1 {
		t.Errorf("two sources into one sink: OtherSources = %d, want 1", res.Findings[0].OtherSources)
	}
	fps := fingerprints(res)
	if len(slices.Compact(fps)) != 3 {
		t.Errorf("identical sink and source twice in one function share a fingerprint: %v", fps)
	}
}

func TestFindingRecord(t *testing.T) {
	res := scanSource(t, handlerJS)
	f := res.Findings[0]
	if f.RuleID != "ts/cmdi" || f.Severity != Critical || f.CWE[0] != "CWE-78" || f.Location.Path != "app.js" {
		t.Errorf("finding = %+v", f)
	}
	if f.Confidence != ConfidenceMedium || !slices.Contains(f.Tags, TagUntypedHandler) || !slices.Contains(f.Tags, TagIntraprocedural) {
		t.Errorf("untyped handler: confidence %s tags %v", f.Confidence, f.Tags)
	}
	if len(f.Trace) < 2 || f.Trace[0].Kind != StepSource || f.Trace[len(f.Trace)-1].Kind != StepSink {
		t.Fatalf("trace = %+v", f.Trace)
	}
	if f.Trace[0].Note != "read `req.query.host` (HTTP query string)" || f.Trace[0].Snippet != `exec("ping " + req.query.host);` {
		t.Errorf("source step = %+v", f.Trace[0])
	}
	want := "User input from `req.query.host` (line 5) reaches `exec` (line 5) without escaping."
	if f.Message != want {
		t.Errorf("message = %q", f.Message)
	}
	if len(f.Fingerprint) != 32 {
		t.Errorf("fingerprint %q", f.Fingerprint)
	}
}
