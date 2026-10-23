package report

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/patchd/patchd/engine"
	"github.com/patchd/patchd/engine/rules"
	"github.com/patchd/patchd/engine/tsbridge"
)

// scan scans a small vulnerable JavaScript app: every rule fires once.
func scan(t *testing.T) *engine.Result {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"package.json": `{"name":"app"}`,
		"app.js": `const { exec } = require("child_process");
const fs = require("fs");
const { Pool } = require("pg");
const pool = new Pool();
module.exports.h = function (req, res) {
  exec("ping " + req.query.host);
  fs.readFileSync(req.params.file);
  pool.query("SELECT * FROM t WHERE id = " + req.body.id);
};
`,
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	res, err := engine.Analyze(context.Background(), dir, engine.Options{Rules: rules.IDs(), Discover: tsbridge.DiscoverOptions{NoRepoConfig: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 3 {
		t.Fatalf("%d findings, want 3: %+v", len(res.Findings), res.Findings)
	}
	return res
}

func TestSARIFValidatesAgainstSchema(t *testing.T) {
	res := scan(t)
	var buf bytes.Buffer
	if err := SARIF(&buf, res, "v0.4.0"); err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	schema, err := c.Compile(filepath.Join("testdata", "sarif-schema-2.1.0.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(doc); err != nil {
		t.Fatalf("SARIF does not validate:\n%v\n%s", err, buf.String())
	}

	var log struct {
		Version string `json:"version"`
		Runs    []struct {
			Tool struct {
				Driver struct {
					Name  string `json:"name"`
					Rules []struct {
						ID         string         `json:"id"`
						Help       map[string]any `json:"help"`
						Properties map[string]any `json:"properties"`
					} `json:"rules"`
				} `json:"driver"`
			} `json:"tool"`
			Results []struct {
				RuleID              string            `json:"ruleId"`
				Level               string            `json:"level"`
				PartialFingerprints map[string]string `json:"partialFingerprints"`
				CodeFlows           []struct {
					ThreadFlows []struct {
						Locations []any `json:"locations"`
					} `json:"threadFlows"`
				} `json:"codeFlows"`
				Properties map[string]any `json:"properties"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(buf.Bytes(), &log); err != nil {
		t.Fatal(err)
	}
	run := log.Runs[0]
	if log.Version != "2.1.0" || run.Tool.Driver.Name != "Patchd" || len(run.Tool.Driver.Rules) != 3 {
		t.Fatalf("unexpected log header: %+v", log)
	}
	for _, r := range run.Tool.Driver.Rules {
		if r.Help["markdown"] == nil || r.Properties["security-severity"] == nil {
			t.Errorf("rule %s lacks help or security-severity", r.ID)
		}
	}
	for _, r := range run.Results {
		if r.PartialFingerprints[FingerprintKey] == "" || len(r.CodeFlows) != 1 || len(r.CodeFlows[0].ThreadFlows[0].Locations) < 2 {
			t.Errorf("result %s: fingerprints %v, code flows %+v", r.RuleID, r.PartialFingerprints, r.CodeFlows)
		}
		if r.Level != "error" || r.Properties["security-severity"] == nil {
			t.Errorf("result %s: level %s, properties %v", r.RuleID, r.Level, r.Properties)
		}
	}
}

func TestJSON(t *testing.T) {
	res := scan(t)
	var buf bytes.Buffer
	if err := JSON(&buf, res, "v0.4.0"); err != nil {
		t.Fatal(err)
	}
	var doc Document
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Schema != "patchd.findings/v1" || len(doc.Findings) != 3 || len(doc.Rules) != 3 || doc.Tool.Version != "v0.4.0" {
		t.Errorf("document = %+v", doc)
	}
	if !strings.Contains(buf.String(), `"fingerprint"`) || !strings.Contains(buf.String(), `"trace"`) {
		t.Error("findings lack fingerprint or trace")
	}
}

func TestJSONEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, &engine.Result{}, "dev"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"findings": []`) {
		t.Errorf("no findings must be an empty list:\n%s", buf.String())
	}
}

func TestPretty(t *testing.T) {
	res := scan(t)
	var plain, color bytes.Buffer
	if err := Pretty(&plain, res, PrettyOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := Pretty(&color, res, PrettyOptions{Color: true, Verbose: true}); err != nil {
		t.Fatal(err)
	}
	out := plain.String()
	for _, want := range []string{"app.js\n", "CRITICAL ts/cmdi Command injection", "1. 6:", "│ exec(\"ping \" + req.query.host);", "3 findings (2 critical, 1 high)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("colour codes without Color")
	}
	if !strings.Contains(color.String(), "\x1b[") || !strings.Contains(color.String(), "fingerprint") {
		t.Error("Color/Verbose had no effect")
	}
}
