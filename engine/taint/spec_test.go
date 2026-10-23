package taint

import (
	"reflect"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

func sampleSpec() *Spec {
	s := &Spec{}
	s.AddSource(Source{ID: "express.req.query", Match: Matcher{QualifiedName: "express:Request.query", Access: AccessProperty}, Kinds: []Kind{KindUserInput}, Desc: "query string"})
	s.AddSink(Sink{ID: "node.child_process.exec", Match: Matcher{QualifiedName: "node:child_process.exec", Access: AccessCall}, Args: []int{0}, Kind: KindCommand, Rule: "ts/cmdi"})
	s.AddSanitizer(Sanitizer{ID: "node.path.basename", Match: Matcher{QualifiedName: "node:path.basename", Also: []string{"global:path.basename"}, Access: AccessCall}, Clears: []Kind{KindPath}})
	s.AddPropagator(Propagator{ID: "array.push", Match: Matcher{QualifiedName: "global:Array.push", Access: AccessCall}, From: []Slot{SlotArgs}, To: []Slot{SlotReceiver}})
	return s
}

func TestSpecLookups(t *testing.T) {
	s := sampleSpec()
	if err := s.Validate(func(id string) bool { return id == "ts/cmdi" }); err != nil {
		t.Fatal(err)
	}
	if got := s.SourcesFor("express:Request.query", AccessProperty); len(got) != 1 || got[0].ID != "express.req.query" {
		t.Errorf("SourcesFor = %v", got)
	}
	if got := s.SourcesFor("express:Request.query", AccessCall); len(got) != 0 {
		t.Errorf("SourcesFor with the wrong access = %v", got)
	}
	if got := s.SinksFor("node:child_process.exec"); len(got) != 1 || got[0].Kind != KindCommand {
		t.Errorf("SinksFor = %v", got)
	}
	if got := s.SanitizersFor("global:path.basename"); len(got) != 1 {
		t.Errorf("SanitizersFor(alias) = %v", got)
	}
	if got := s.PropagatorsFor("global:Array.push"); len(got) != 1 {
		t.Errorf("PropagatorsFor = %v", got)
	}
	if !s.IsSourceMember("query") || s.IsSourceMember("body") {
		t.Error("IsSourceMember")
	}
	if got := s.Rules(); !reflect.DeepEqual(got, []string{"ts/cmdi"}) {
		t.Errorf("Rules = %v", got)
	}
}

func TestSpecValidate(t *testing.T) {
	s := sampleSpec()
	s.AddSource(Source{ID: "express.req.query", Match: Matcher{QualifiedName: "x", Access: "nope"}, Kinds: []Kind{KindSQL}})
	s.AddSink(Sink{ID: "bad.sink", Match: Matcher{Access: AccessCall}, Args: []int{-1}, Kind: KindUserInput, Rule: "ts/unknown"})
	s.AddSanitizer(Sanitizer{ID: "bad.san", Match: Matcher{QualifiedName: "y", Access: AccessCall}, Clears: []Kind{"html5"}, On: "nowhere"})
	s.AddPropagator(Propagator{ID: "bad.prop", Match: Matcher{QualifiedName: "z", Access: AccessCall}, From: []Slot{"arg:-1"}})
	err := s.Validate(func(id string) bool { return id == "ts/cmdi" })
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{
		`duplicate id "express.req.query"`, `access "nope"`, `"sql" is not an origin kind`,
		"matcher without a qualified name", "negative argument -1", `"user-input" is not a sink kind`,
		`unknown rule "ts/unknown"`, `"html5" is not a sink kind`, `unknown slot "nowhere"`,
		`unknown slot "arg:-1"`, "no to slots",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}
}

func TestKindSet(t *testing.T) {
	s := KindSetOf(KindSQL, KindPath, KindUserInput)
	if !s.Has(KindSQL) || !s.Has(KindPath) || s.Has(KindCommand) || s.Has(KindUserInput) {
		t.Errorf("Has: %v", s)
	}
	if got := s.String(); got != "{sql,path}" {
		t.Errorf("String = %q", got)
	}
	if AllSinkKinds&^NumericPruned != KindSetOf(KindHTML, KindCode) {
		t.Errorf("numeric pruning leaves %v", AllSinkKinds&^NumericPruned)
	}
	for _, k := range []Kind{KindUserInput, KindEnvironment, KindSQL, KindCommand, KindPath, KindURL, KindHTML, KindCode} {
		if !k.Valid() {
			t.Errorf("%s invalid", k)
		}
	}
	if Kind("xss").Valid() {
		t.Error("unknown kind valid")
	}
}

func TestSlots(t *testing.T) {
	if i, ok := Arg(3).ArgIndex(); !ok || i != 3 {
		t.Errorf("Arg(3) = %v %v", i, ok)
	}
	for _, s := range []Slot{SlotReturn, SlotReceiver, SlotArgs, Arg(0)} {
		if !s.Valid() {
			t.Errorf("%s invalid", s)
		}
	}
	if Slot("arg:x").Valid() || Slot("this").Valid() {
		t.Error("bad slot valid")
	}
}

// The spec types become YAML-loadable in v0.6: they must round-trip.
func TestSpecYAMLRoundTrip(t *testing.T) {
	s := sampleSpec()
	data, err := yaml.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back Spec
	if err := yaml.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	s.idx = nil
	if !reflect.DeepEqual(*s, back) {
		t.Errorf("round trip differs:\n%s", data)
	}
	if !strings.Contains(string(data), "qualified_name: express:Request.query") {
		t.Errorf("unexpected YAML:\n%s", data)
	}
}
