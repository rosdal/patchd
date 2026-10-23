package security

import (
	"strings"

	"github.com/patchd/patchd/engine/ir"
	"github.com/patchd/patchd/engine/models"
	"github.com/patchd/patchd/engine/rules"
	"github.com/patchd/patchd/engine/taint"
)

func init() {
	rules.Register(&rules.Rule{
		ID:       models.RuleCmdi,
		Name:     "Command injection",
		CWE:      []string{"CWE-78"},
		Severity: rules.Critical,
		Kind:     taint.KindCommand,
		Short:    "User input reaches a shell command.",
		Long: "User-controlled data reaches `child_process.exec`, `execSync` or the command of `spawn`/`execFile`. " +
			"`exec` runs its argument in a shell, so characters like `;`, `|`, `$( )` and backticks let an attacker " +
			"run any command with the server's privileges.\n\n" +
			"`spawn` and `execFile` with an argument array don't use a shell: their arguments are only reported " +
			"with `shell: true`. The command itself (argument 0) is always reported.",
		Fix: "Don't build shell commands from input. Run the program directly with an argument array, " +
			"and validate the arguments against an allowlist:\n\n" +
			"```ts\n" +
			"// Unsafe\n" +
			"exec(`ping -c 1 ${req.query.host}`);\n\n" +
			"// Safe: no shell, the host is one argument\n" +
			"execFile(\"ping\", [\"-c\", \"1\", String(req.query.host)]);\n" +
			"```\n\n" +
			"Also reject values starting with `-`, which the program would read as options.",
		Advice: "without escaping",
		Keep:   keepCmdi,
	})
}

// keepCmdi drops the argument array of spawn and execFile unless the call
// passes shell: true.
func keepCmdi(c *rules.Candidate) bool {
	id := c.Taint.Sink.ID
	if c.Taint.Arg != 1 || !strings.Contains(id, "spawn") && !strings.Contains(id, "execFile") {
		return true
	}
	f := c.Func()
	args := c.Args()
	if len(args) < 3 {
		return false
	}
	opts := def(f, args[2])
	if opts == nil || opts.Op != ir.OpObject { // options we can't read: stay quiet
		return false
	}
	shell, ok := objectProperty(f, opts, "shell")
	return ok && constTruthy(f, shell)
}
