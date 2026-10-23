package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeApp writes a one-file JavaScript app; it needs no node_modules.
func writeApp(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range map[string]string{"package.json": `{"name":"app"}`, "app.js": src} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const vulnerableApp = `const { exec } = require("child_process");
const fs = require("fs");
module.exports.h = function (req, res) {
  exec("ping " + req.query.host);
  fs.readFileSync(req.params.file);
};
`

func runCLI(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := newRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	var ee *exitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		code = ee.code
	default:
		code = 1
	}
	return out.String(), errOut.String(), code
}

func TestScanExitCodes(t *testing.T) {
	vuln := writeApp(t, vulnerableApp)
	clean := writeApp(t, `module.exports.h = function (req, res) { res.send("ok"); };`)
	tests := []struct {
		name string
		args []string
		code int
	}{
		{"findings", []string{"scan", vuln}, exitFindings},
		{"clean", []string{"scan", clean}, exitClean},
		{"fail-on above the findings", []string{"scan", vuln, "--rules", "ts/path-traversal", "--fail-on", "critical"}, exitClean},
		{"severity threshold hides findings", []string{"scan", vuln, "--severity-threshold", "critical", "--rules", "ts/path-traversal"}, exitClean},
		{"bad format", []string{"scan", vuln, "--format", "xml"}, exitUsage},
		{"unknown rule", []string{"scan", vuln, "--rules", "ts/nope"}, exitUsage},
		{"bad severity", []string{"scan", vuln, "--fail-on", "urgent"}, exitUsage},
		{"missing path", []string{"scan", filepath.Join(vuln, "nope")}, exitUsage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, code := runCLI(t, tt.args...)
			if code != tt.code {
				t.Errorf("exit %d, want %d; stderr:\n%s", code, tt.code, stderr)
			}
		})
	}
}

func TestScanFormats(t *testing.T) {
	vuln := writeApp(t, vulnerableApp)
	stdout, stderr, _ := runCLI(t, "scan", vuln, "--format", "json")
	var doc struct {
		Schema   string `json:"schema"`
		Findings []struct {
			Rule string `json:"rule"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("json output: %v\n%s", err, stdout)
	}
	if doc.Schema != "patchd.findings/v1" || len(doc.Findings) != 2 {
		t.Errorf("json = %+v", doc)
	}
	if !strings.Contains(stderr, "2 findings") || !strings.Contains(stderr, "load ") {
		t.Errorf("stderr lacks progress or summary:\n%s", stderr)
	}

	out := filepath.Join(t.TempDir(), "patchd.sarif")
	stdout, _, code := runCLI(t, "scan", vuln, "--format", "sarif", "--output", out, "--rules", "ts/cmdi")
	if code != exitFindings || stdout != "" {
		t.Errorf("exit %d, stdout %q", code, stdout)
	}
	data, err := os.ReadFile(out) //nolint:gosec // a test temp file
	if err != nil || !strings.Contains(string(data), `"version": "2.1.0"`) || strings.Contains(string(data), "path-traversal\"") {
		t.Errorf("sarif file: %v\n%.300s", err, data)
	}

	stdout, _, _ = runCLI(t, "scan", vuln)
	if !strings.Contains(stdout, "CRITICAL ts/cmdi") || strings.Contains(stdout, "\x1b[") {
		t.Errorf("pretty output (no colour into a buffer):\n%s", stdout)
	}
}

func TestRulesList(t *testing.T) {
	stdout, _, code := runCLI(t, "rules", "list")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, id := range []string{"ts/cmdi", "ts/path-traversal", "ts/sqli", "CWE-89"} {
		if !strings.Contains(stdout, id) {
			t.Errorf("missing %s:\n%s", id, stdout)
		}
	}
}
