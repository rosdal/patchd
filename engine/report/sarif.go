package report

import (
	"io"
	"strings"

	"github.com/owenrumney/go-sarif/v2/sarif"

	"github.com/patchd/patchd/engine"
	"github.com/patchd/patchd/engine/rules"
)

// InformationURI is where the SARIF driver points for documentation.
const InformationURI = "https://github.com/rosdal/patchd"

// FingerprintKey names Patchd's fingerprint in partialFingerprints.
const FingerprintKey = "patchdFingerprint/v1"

// securitySeverity is GitHub's 0–10 score for code scanning severity
// (critical ≥ 9.0, high ≥ 7.0, medium ≥ 4.0, low below).
var securitySeverity = map[rules.Severity]string{
	rules.Critical: "9.5",
	rules.High:     "8.0",
	rules.Medium:   "5.5",
	rules.Low:      "3.0",
}

// level is the SARIF result level for a severity.
func level(s rules.Severity) string {
	switch s {
	case rules.Critical, rules.High:
		return "error"
	case rules.Medium:
		return "warning"
	}
	return "note"
}

// SARIF writes a SARIF 2.1.0 log: one run whose driver lists the rules that
// ran with their help text, and one result per finding with its location,
// the trace as a code flow, the fingerprint and a security-severity GitHub
// code scanning understands.
func SARIF(w io.Writer, res *engine.Result, version string) error {
	log, err := sarif.New(sarif.Version210, true)
	if err != nil {
		return err
	}
	driver := sarif.NewVersionedDriver("Patchd", version).WithInformationURI(InformationURI)
	driver.WithSemanticVersion(strings.TrimPrefix(version, "v"))
	run := sarif.NewRun(*sarif.NewTool(driver))
	ruleIndex := map[string]int{}
	for i, r := range res.Rules {
		ruleIndex[r.ID] = i
		desc := sarif.NewRule(r.ID).
			WithName(ruleName(r.Name)).
			WithShortDescription(sarif.NewMultiformatMessageString(r.Short)).
			WithFullDescription(sarif.NewMultiformatMessageString(plain(r.Long))).
			WithHelp(sarif.NewMultiformatMessageString(plain(r.Long + "\n\n" + r.Fix)).WithMarkdown(r.Long + "\n\n" + r.Fix)).
			WithDefaultConfiguration(sarif.NewReportingConfiguration().WithLevel(level(r.Severity)))
		tags := []string{"security"}
		for _, cwe := range r.CWE {
			tags = append(tags, "external/cwe/"+strings.ToLower(cwe))
		}
		desc.WithProperties(sarif.Properties{
			"tags":              tags,
			"precision":         "high",
			"problem.severity":  level(r.Severity),
			"security-severity": securitySeverity[r.Severity],
		})
		driver.AddRule(desc)
	}
	run.Tool.Driver.Rules = driver.Rules
	for _, f := range res.Findings {
		result := sarif.NewRuleResult(f.RuleID).
			WithLevel(level(f.Severity)).
			WithMessage(sarif.NewTextMessage(f.Message)).
			WithLocations([]*sarif.Location{location(f.Location, "")})
		if i, ok := ruleIndex[f.RuleID]; ok {
			result.WithRuleIndex(i)
		}
		result.WithPartialFingerPrints(map[string]any{FingerprintKey: f.Fingerprint})
		flow := sarif.NewThreadFlow()
		for _, st := range f.Trace {
			tfl := sarif.NewThreadFlowLocation().WithLocation(location(st.Location, st.Note))
			tfl.WithKinds([]string{stepKind(st.Kind)})
			flow.AddLocation(tfl)
		}
		result.AddCodeFlow(sarif.NewCodeFlow().WithThreadFlows([]*sarif.ThreadFlow{flow}))
		props := sarif.Properties{
			"security-severity": securitySeverity[f.Severity],
			"confidence":        string(f.Confidence),
		}
		if len(f.Tags) > 0 {
			props["tags"] = f.Tags
		}
		if f.OtherSources > 0 {
			props["otherSources"] = f.OtherSources
		}
		result.Properties = props
		run.AddResult(result)
	}
	log.AddRun(run)
	return log.PrettyWrite(w)
}

func location(l engine.Location, note string) *sarif.Location {
	region := sarif.NewRegion().WithStartLine(l.StartLine).WithStartColumn(l.StartColumn)
	if l.EndLine > 0 {
		region.WithEndLine(l.EndLine).WithEndColumn(l.EndColumn)
	}
	loc := sarif.NewLocationWithPhysicalLocation(sarif.NewPhysicalLocation().
		WithArtifactLocation(sarif.NewSimpleArtifactLocation(l.Path).WithUriBaseId("%SRCROOT%")).
		WithRegion(region))
	if note != "" {
		loc.WithMessage(sarif.NewTextMessage(note))
	}
	return loc
}

// stepKind maps a trace step to SARIF's threadFlowLocation kinds.
func stepKind(k engine.StepKind) string {
	switch k {
	case engine.StepSource:
		return "taint-source"
	case engine.StepSink:
		return "taint-sink"
	case engine.StepSanitizer:
		return "sanitize"
	}
	return "taint-propagate"
}

// ruleName turns "SQL injection" into "SqlInjection", the PascalCase SARIF
// recommends for reportingDescriptor.name.
func ruleName(name string) string {
	var b strings.Builder
	for _, w := range strings.Fields(name) {
		b.WriteString(strings.ToUpper(w[:1]) + strings.ToLower(w[1:]))
	}
	return b.String()
}

// plain strips Markdown's code markers for text-only fields.
func plain(md string) string {
	return strings.ReplaceAll(md, "`", "")
}
