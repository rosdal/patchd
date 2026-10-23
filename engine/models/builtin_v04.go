// Built-in v0.4 models: Express, Node core and pg.

package models

import "github.com/patchd/patchd/engine/taint"

// The v0.4 models are hard-coded here and move to defs/*.yaml in v0.6, when
// the loader exists. They are deliberately small: every entry has a fixture
// under engine/testdata/fixtures.

// Rule IDs the built-in sinks report under.
const (
	RuleCmdi          = "ts/cmdi"
	RulePathTraversal = "ts/path-traversal"
	RuleSQLi          = "ts/sqli"
)

// Builtin returns a fresh spec with the v0.4 models.
func Builtin() *taint.Spec {
	s := &taint.Spec{}
	addExpress(s)
	addNodeCore(s)
	addPg(s)
	addGeneric(s)
	return s
}

func property(qn string, also ...string) taint.Matcher {
	return taint.Matcher{QualifiedName: qn, Also: also, Access: taint.AccessProperty}
}

func call(qn string, also ...string) taint.Matcher {
	return taint.Matcher{QualifiedName: qn, Also: also, Access: taint.AccessCall}
}

var userInput = []taint.Kind{taint.KindUserInput}

func addExpress(s *taint.Spec) {
	for _, p := range []struct{ name, desc string }{
		{"query", "HTTP query string"},
		{"body", "HTTP request body"},
		{"params", "URL route parameters"},
		{"headers", "HTTP request headers"},
		{"cookies", "HTTP cookies"},
		{"path", "URL path"},
		{"originalUrl", "request URL"},
		{"url", "request URL"},
		{"hostname", "Host header"},
	} {
		s.AddSource(taint.Source{
			ID:    "express.req." + p.name,
			Match: property("express:Request." + p.name),
			Kinds: userInput,
			Desc:  p.desc,
		})
	}
	for _, m := range []struct{ name, desc string }{
		{"get", "HTTP request header"},
		{"header", "HTTP request header"},
		{"param", "request parameter (legacy req.param)"},
	} {
		s.AddSource(taint.Source{
			ID:    "express.req." + m.name + "()",
			Match: call("express:Request." + m.name),
			Kinds: userInput,
			Desc:  m.desc,
		})
	}
	for _, m := range []string{"sendFile", "download"} {
		s.AddSink(taint.Sink{
			ID:    "express.res." + m,
			Match: call("express:Response." + m),
			Args:  []int{0},
			Kind:  taint.KindPath,
			Rule:  RulePathTraversal,
			Desc:  "sends a file from the server's file system",
		})
	}
}

func addNodeCore(s *taint.Spec) {
	// Raw http servers; Express's Request extends IncomingMessage, so
	// req.url and req.headers resolve here too.
	s.AddSource(
		taint.Source{ID: "node.http.req.url", Match: property("node:http.IncomingMessage.url", "node:https.IncomingMessage.url"), Kinds: userInput, Desc: "request URL"},
		taint.Source{ID: "node.http.req.headers", Match: property("node:http.IncomingMessage.headers", "node:https.IncomingMessage.headers"), Kinds: userInput, Desc: "HTTP request headers"},
		taint.Source{ID: "node.process.argv", Match: property("global:NodeJS.Process.argv", "global:process.argv"), Kinds: []taint.Kind{taint.KindEnvironment}, Desc: "command-line argument"},
		taint.Source{ID: "node.process.env", Match: property("global:NodeJS.Process.env", "global:process.env"), Kinds: []taint.Kind{taint.KindEnvironment}, Desc: "environment variable"},
	)

	for _, fn := range []string{"exec", "execSync"} {
		s.AddSink(taint.Sink{
			ID: "node.child_process." + fn, Match: call("node:child_process." + fn),
			Args: []int{0}, Kind: taint.KindCommand, Rule: RuleCmdi, Desc: "runs a command in a shell",
		})
	}
	// Argument 1 (the argument array) only matters with shell: true; the
	// ts/cmdi rule checks that.
	for _, fn := range []string{"spawn", "spawnSync", "execFile", "execFileSync"} {
		s.AddSink(taint.Sink{
			ID: "node.child_process." + fn, Match: call("node:child_process." + fn),
			Args: []int{0, 1}, Kind: taint.KindCommand, Rule: RuleCmdi, Desc: "starts a process",
		})
	}

	// fs: every function whose first argument is a path it reads, writes,
	// inspects or removes. Copies and renames take two paths.
	paths := map[string][]int{
		"readFile": {0}, "writeFile": {0}, "appendFile": {0}, "stat": {0}, "lstat": {0}, "unlink": {0},
		"readdir": {0}, "open": {0}, "access": {0}, "rm": {0}, "rmdir": {0}, "mkdir": {0},
		"readlink": {0}, "realpath": {0}, "truncate": {0}, "chmod": {0}, "chown": {0},
		"rename": {0, 1}, "copyFile": {0, 1}, "cp": {0, 1}, "symlink": {0, 1}, "link": {0, 1},
	}
	for fn, args := range paths {
		for _, mod := range []string{"fs", "fs/promises"} {
			s.AddSink(taint.Sink{
				ID: "node." + mod + "." + fn, Match: call("node:" + mod + "." + fn),
				Args: args, Kind: taint.KindPath, Rule: RulePathTraversal, Desc: "accesses the file system",
			})
		}
		s.AddSink(taint.Sink{
			ID: "node.fs." + fn + "Sync", Match: call("node:fs." + fn + "Sync"),
			Args: args, Kind: taint.KindPath, Rule: RulePathTraversal, Desc: "accesses the file system",
		})
	}
	for _, fn := range []string{"createReadStream", "createWriteStream", "existsSync", "exists"} {
		s.AddSink(taint.Sink{
			ID: "node.fs." + fn, Match: call("node:fs." + fn),
			Args: []int{0}, Kind: taint.KindPath, Rule: RulePathTraversal, Desc: "accesses the file system",
		})
	}

	// @types/node declares path as `declare module "path" { namespace path
	// … export = path }`, which tsbridge names global:path.*; the node:
	// spelling is what import provenance produces.
	s.AddSanitizer(taint.Sanitizer{
		ID:     "node.path.basename",
		Match:  call("node:path.basename", "global:path.basename", "node:path/posix.basename", "node:path/win32.basename"),
		Clears: []taint.Kind{taint.KindPath},
		Desc:   "strips directories, so the value can't climb out of a base directory",
	})
}

func addPg(s *taint.Spec) {
	// Argument 0 when it is query text; the ts/sqli rule ignores a
	// { text, values } config, whose values are sent separately.
	for _, owner := range []string{"Client", "Pool", "PoolClient"} {
		s.AddSink(taint.Sink{
			ID: "pg." + owner + ".query", Match: call("pg:" + owner + ".query"),
			Args: []int{0}, Kind: taint.KindSQL, Rule: RuleSQLi, Desc: "runs an SQL query",
		})
	}
}

func addGeneric(s *taint.Spec) {
	for _, fn := range []string{"push", "unshift", "splice"} {
		s.AddPropagator(taint.Propagator{
			ID:    "js.Array." + fn,
			Match: call("global:Array." + fn),
			From:  []taint.Slot{taint.SlotArgs},
			To:    []taint.Slot{taint.SlotReceiver},
			Desc:  "adds elements to the array",
		})
	}
}
