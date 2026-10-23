// Source, sink, sanitizer and propagator types.

package taint

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// The spec types are written in Go for v0.4 and loaded from YAML in v0.6:
// every field is a plain value with a yaml tag, and nothing refers to code.

// Kind is a taint kind (ADR 0010). Origin kinds say where data came from;
// sink kinds say which dangerous operations it can still reach.
type Kind string

// Taint kinds.
const (
	KindUserInput   Kind = "user-input"  // origin: HTTP requests and other remote input
	KindEnvironment Kind = "environment" // origin: process.argv, process.env (low severity, off by default)

	KindSQL     Kind = "sql"
	KindCommand Kind = "command"
	KindPath    Kind = "path"
	KindURL     Kind = "url"
	KindHTML    Kind = "html"
	KindCode    Kind = "code"
)

// sinkKinds lists the sink kinds in bit order for KindSet.
var sinkKinds = [...]Kind{KindSQL, KindCommand, KindPath, KindURL, KindHTML, KindCode}

// IsOrigin reports whether k is an origin kind.
func (k Kind) IsOrigin() bool { return k == KindUserInput || k == KindEnvironment }

// IsSink reports whether k is a sink kind.
func (k Kind) IsSink() bool { return slices.Contains(sinkKinds[:], k) }

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool { return k.IsOrigin() || k.IsSink() }

// KindSet is a set of sink kinds.
type KindSet uint8

// AllSinkKinds is every sink kind: what a fresh source label can reach.
const AllSinkKinds KindSet = 1<<len(sinkKinds) - 1

// NumericPruned are the sink kinds a number or boolean can't inject into.
var NumericPruned = KindSetOf(KindSQL, KindCommand, KindPath, KindURL)

// KindSetOf returns the set of the sink kinds among ks.
func KindSetOf(ks ...Kind) KindSet {
	var s KindSet
	for _, k := range ks {
		if i := slices.Index(sinkKinds[:], k); i >= 0 {
			s |= 1 << i
		}
	}
	return s
}

// Has reports whether s contains k.
func (s KindSet) Has(k Kind) bool { return s&KindSetOf(k) != 0 && k.IsSink() }

// Kinds returns the kinds in s, in a fixed order.
func (s KindSet) Kinds() []Kind {
	var out []Kind
	for i, k := range sinkKinds {
		if s&(1<<i) != 0 {
			out = append(out, k)
		}
	}
	return out
}

func (s KindSet) String() string {
	ks := s.Kinds()
	parts := make([]string, len(ks))
	for i, k := range ks {
		parts[i] = string(k)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// Access is the shape of code a Matcher selects.
type Access string

// Access shapes.
const (
	AccessCall      Access = "call"      // a call or new of the named function or method
	AccessProperty  Access = "property"  // a read of the named property
	AccessParameter Access = "parameter" // a parameter whose type is the named type
	AccessDecorator Access = "decorator" // a parameter decorated with the named decorator (v0.6: NestJS)
)

// Matcher selects IR values by tsbridge qualified name (ADR 0008) and access
// shape. Calls resolve through tsbridge.ResolveCall, property reads through
// the property's symbol or the receiver's type, and both through import
// provenance when the code has no types (ADR 0010), so
// `const { exec } = require('child_process')` and
// `import cp from 'node:child_process'` both match node:child_process.exec.
type Matcher struct {
	QualifiedName string   `yaml:"qualified_name"`          // "express:Request.query", "node:child_process.exec"
	Also          []string `yaml:"also,omitempty"`          // other names for the same thing ("global:path.basename" for node:path.basename)
	Access        Access   `yaml:"access"`                  // call, property, parameter, decorator
	ReceiverType  string   `yaml:"receiver_type,omitempty"` // optional: the receiver's TypeQualifiedName must be this
}

// Names returns the matcher's qualified name and its alternatives.
func (m Matcher) Names() []string {
	return append([]string{m.QualifiedName}, m.Also...)
}

// Source is where untrusted data enters.
type Source struct {
	ID    string  `yaml:"id"`    // "express.req.query"
	Match Matcher `yaml:"match"` // what IR value or access this applies to
	Kinds []Kind  `yaml:"kinds"` // origin kinds, usually ["user-input"]
	Desc  string  `yaml:"desc"`  // "HTTP query string parameter"
}

// Origin returns the source's origin kind: environment if listed, else
// user-input.
func (s *Source) Origin() Kind {
	if slices.Contains(s.Kinds, KindEnvironment) {
		return KindEnvironment
	}
	return KindUserInput
}

// Sink is where tainted data is dangerous.
type Sink struct {
	ID    string  `yaml:"id"`    // "node.child_process.exec"
	Match Matcher `yaml:"match"` // a call to this qualified name
	Args  []int   `yaml:"args"`  // which arguments are dangerous (0-based, receiver excluded)
	Kind  Kind    `yaml:"kind"`  // "command"
	Rule  string  `yaml:"rule"`  // rule ID that reports it: "ts/cmdi"
	Desc  string  `yaml:"desc"`  // "runs a shell command"
}

// Slot names a place taint moves from or to at a call.
type Slot string

// Slots. Arg(i) builds "arg:i".
const (
	SlotReturn   Slot = "return"   // the call's result
	SlotReceiver Slot = "receiver" // the object a method is called on
	SlotArgs     Slot = "args"     // every argument
)

// Arg returns the slot of the i-th argument (0-based, receiver excluded).
func Arg(i int) Slot { return Slot("arg:" + strconv.Itoa(i)) }

// ArgIndex returns i for an "arg:i" slot.
func (s Slot) ArgIndex() (int, bool) {
	n, ok := strings.CutPrefix(string(s), "arg:")
	if !ok {
		return 0, false
	}
	i, err := strconv.Atoi(n)
	return i, err == nil && i >= 0
}

// Valid reports whether s is a known slot.
func (s Slot) Valid() bool {
	if s == SlotReturn || s == SlotReceiver || s == SlotArgs {
		return true
	}
	_, ok := s.ArgIndex()
	return ok
}

// Sanitizer clears specific kinds from a value.
type Sanitizer struct {
	ID     string  `yaml:"id"`
	Match  Matcher `yaml:"match"`
	Clears []Kind  `yaml:"clears"`
	// On is where the sanitized value is: the return value (default), or an
	// argument for in-place validators (v0.5, with path sensitivity).
	On   Slot   `yaml:"on,omitempty"`
	Desc string `yaml:"desc,omitempty"`
}

// Propagator says how taint moves through a known call when the default
// (receiver and arguments to the result) isn't right.
type Propagator struct {
	ID    string  `yaml:"id"`
	Match Matcher `yaml:"match"`
	From  []Slot  `yaml:"from"` // Arg(0), receiver, args…
	To    []Slot  `yaml:"to"`   // return, receiver, Arg(1)…
	Desc  string  `yaml:"desc,omitempty"`
}

// Spec holds every source, sink, sanitizer and propagator the analysis
// uses, indexed by qualified name. Build it with Add* and call Index (or
// Validate) before lookups; it is read-only and safe to share after that.
type Spec struct {
	Sources     []Source     `yaml:"sources,omitempty"`
	Sinks       []Sink       `yaml:"sinks,omitempty"`
	Sanitizers  []Sanitizer  `yaml:"sanitizers,omitempty"`
	Propagators []Propagator `yaml:"propagators,omitempty"`

	idx *index
}

type index struct {
	sources     map[string][]int // qualified name -> Sources
	sinks       map[string][]int
	sanitizers  map[string][]int
	propagators map[string][]int
	members     map[string]bool // last path segment of every property source: a cheap pre-filter for loads
}

// AddSource adds sources; likewise for the other Add methods. Adding drops
// the index.
func (s *Spec) AddSource(src ...Source) { s.Sources = append(s.Sources, src...); s.idx = nil }

// AddSink adds sinks.
func (s *Spec) AddSink(sk ...Sink) { s.Sinks = append(s.Sinks, sk...); s.idx = nil }

// AddSanitizer adds sanitizers.
func (s *Spec) AddSanitizer(z ...Sanitizer) { s.Sanitizers = append(s.Sanitizers, z...); s.idx = nil }

// AddPropagator adds propagators.
func (s *Spec) AddPropagator(p ...Propagator) {
	s.Propagators = append(s.Propagators, p...)
	s.idx = nil
}

// Index builds the lookup tables. Lookups call it on first use, but a Spec
// shared between goroutines must be indexed first.
func (s *Spec) Index() {
	idx := &index{
		sources:     map[string][]int{},
		sinks:       map[string][]int{},
		sanitizers:  map[string][]int{},
		propagators: map[string][]int{},
		members:     map[string]bool{},
	}
	for i, x := range s.Sources {
		for _, n := range x.Match.Names() {
			idx.sources[n] = append(idx.sources[n], i)
			if x.Match.Access == AccessProperty {
				idx.members[lastSegment(n)] = true
			}
		}
	}
	for i, x := range s.Sinks {
		for _, n := range x.Match.Names() {
			idx.sinks[n] = append(idx.sinks[n], i)
		}
	}
	for i, x := range s.Sanitizers {
		for _, n := range x.Match.Names() {
			idx.sanitizers[n] = append(idx.sanitizers[n], i)
		}
	}
	for i, x := range s.Propagators {
		for _, n := range x.Match.Names() {
			idx.propagators[n] = append(idx.propagators[n], i)
		}
	}
	s.idx = idx
}

func (s *Spec) index() *index {
	if s.idx == nil {
		s.Index()
	}
	return s.idx
}

// lastSegment returns the member name a qualified name ends in:
// "express:Request.query" -> "query".
func lastSegment(qn string) string {
	if i := strings.LastIndexAny(qn, ".:#"); i >= 0 {
		return qn[i+1:]
	}
	return qn
}

// SourcesFor returns the sources matching a qualified name and access.
func (s *Spec) SourcesFor(qn string, access Access) []*Source {
	var out []*Source
	for _, i := range s.index().sources[qn] {
		if s.Sources[i].Match.Access == access {
			out = append(out, &s.Sources[i])
		}
	}
	return out
}

// SinksFor returns the sinks matching a called qualified name.
func (s *Spec) SinksFor(qn string) []*Sink {
	var out []*Sink
	for _, i := range s.index().sinks[qn] {
		out = append(out, &s.Sinks[i])
	}
	return out
}

// SanitizersFor returns the sanitizers matching a called qualified name.
func (s *Spec) SanitizersFor(qn string) []*Sanitizer {
	var out []*Sanitizer
	for _, i := range s.index().sanitizers[qn] {
		out = append(out, &s.Sanitizers[i])
	}
	return out
}

// PropagatorsFor returns the propagators matching a called qualified name.
func (s *Spec) PropagatorsFor(qn string) []*Propagator {
	var out []*Propagator
	for _, i := range s.index().propagators[qn] {
		out = append(out, &s.Propagators[i])
	}
	return out
}

// IsSourceMember reports whether some property source ends in name: loads
// of other properties never need resolving.
func (s *Spec) IsSourceMember(name string) bool { return s.index().members[name] }

// Rules returns the rule IDs the sinks name, sorted.
func (s *Spec) Rules() []string {
	var out []string
	for _, k := range s.Sinks {
		out = append(out, k.Rule)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Validate checks the spec: IDs are unique and non-empty, every kind is
// known and of the right sort, every sink names a rule that ruleExists
// accepts (nil accepts any non-empty rule), argument indexes and slots are
// valid, and every matcher has a qualified name and access. It indexes the
// spec as a side effect.
func (s *Spec) Validate(ruleExists func(id string) bool) error {
	var errs []error
	seen := map[string]bool{}
	id := func(what, id string) {
		switch {
		case id == "":
			errs = append(errs, fmt.Errorf("%s without an id", what))
		case seen[id]:
			errs = append(errs, fmt.Errorf("duplicate id %q", id))
		}
		seen[id] = true
	}
	match := func(id string, m Matcher, accesses ...Access) {
		if m.QualifiedName == "" {
			errs = append(errs, fmt.Errorf("%s: matcher without a qualified name", id))
		}
		if !slices.Contains(accesses, m.Access) {
			errs = append(errs, fmt.Errorf("%s: access %q, want one of %v", id, m.Access, accesses))
		}
	}
	slots := func(id string, list []Slot, what string) {
		if len(list) == 0 {
			errs = append(errs, fmt.Errorf("%s: no %s slots", id, what))
		}
		for _, sl := range list {
			if !sl.Valid() {
				errs = append(errs, fmt.Errorf("%s: unknown slot %q", id, sl))
			}
		}
	}
	for _, x := range s.Sources {
		id("source", x.ID)
		match(x.ID, x.Match, AccessProperty, AccessCall, AccessParameter, AccessDecorator)
		if len(x.Kinds) == 0 {
			errs = append(errs, fmt.Errorf("%s: no kinds", x.ID))
		}
		for _, k := range x.Kinds {
			if !k.IsOrigin() {
				errs = append(errs, fmt.Errorf("%s: %q is not an origin kind", x.ID, k))
			}
		}
	}
	for _, x := range s.Sinks {
		id("sink", x.ID)
		match(x.ID, x.Match, AccessCall)
		if !x.Kind.IsSink() {
			errs = append(errs, fmt.Errorf("%s: %q is not a sink kind", x.ID, x.Kind))
		}
		if len(x.Args) == 0 {
			errs = append(errs, fmt.Errorf("%s: no dangerous arguments", x.ID))
		}
		for _, a := range x.Args {
			if a < 0 {
				errs = append(errs, fmt.Errorf("%s: negative argument %d", x.ID, a))
			}
		}
		switch {
		case x.Rule == "":
			errs = append(errs, fmt.Errorf("%s: no rule", x.ID))
		case ruleExists != nil && !ruleExists(x.Rule):
			errs = append(errs, fmt.Errorf("%s: unknown rule %q", x.ID, x.Rule))
		}
	}
	for _, x := range s.Sanitizers {
		id("sanitizer", x.ID)
		match(x.ID, x.Match, AccessCall)
		if len(x.Clears) == 0 {
			errs = append(errs, fmt.Errorf("%s: clears nothing", x.ID))
		}
		for _, k := range x.Clears {
			if !k.IsSink() {
				errs = append(errs, fmt.Errorf("%s: %q is not a sink kind", x.ID, k))
			}
		}
		if x.On != "" && !x.On.Valid() {
			errs = append(errs, fmt.Errorf("%s: unknown slot %q", x.ID, x.On))
		}
	}
	for _, x := range s.Propagators {
		id("propagator", x.ID)
		match(x.ID, x.Match, AccessCall)
		slots(x.ID, x.From, "from")
		slots(x.ID, x.To, "to")
	}
	s.Index()
	return errors.Join(errs...)
}
