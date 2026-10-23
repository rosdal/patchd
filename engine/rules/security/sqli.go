package security

import (
	"github.com/patchd/patchd/engine/ir"
	"github.com/patchd/patchd/engine/models"
	"github.com/patchd/patchd/engine/rules"
	"github.com/patchd/patchd/engine/taint"
	"github.com/patchd/patchd/engine/tsbridge"
)

func init() {
	rules.Register(&rules.Rule{
		ID:       models.RuleSQLi,
		Name:     "SQL injection",
		CWE:      []string{"CWE-89"},
		Severity: rules.Critical,
		Kind:     taint.KindSQL,
		Short:    "User input is built into the text of an SQL query.",
		Long: "User-controlled data is concatenated or interpolated into an SQL query string and run with `pg`. " +
			"An attacker can close the string literal and append SQL of their own: read other users' rows, " +
			"bypass a login check, or change and delete data.\n\n" +
			"The rule reports a query whose text is built from a request value with `+`, `+=`, a template literal or " +
			"a string-building call (`concat`, `join`, `replace`, `format`) in the same function. " +
			"A `{ text, values }` query config is not reported: its values are sent separately.",
		Fix: "Pass user input as query parameters, never as query text:\n\n" +
			"```ts\n" +
			"// Unsafe\n" +
			"await pool.query(`SELECT * FROM users WHERE id = ${req.params.id}`);\n\n" +
			"// Safe: $1 is bound by the database driver\n" +
			"await pool.query(\"SELECT * FROM users WHERE id = $1\", [req.params.id]);\n" +
			"```\n\n" +
			"Identifiers (table and column names) can't be parameters: map them from a fixed allowlist.",
		Advice: "without parameterisation",
		Keep:   keepSQLi,
	})
}

// keepSQLi reports only query text built from the tainted part, and never a
// query config object.
func keepSQLi(c *rules.Candidate) bool {
	f := c.Func()
	if in := def(f, c.Taint.Value); in != nil && in.Op == ir.OpObject {
		return false
	}
	if v := f.Value(c.Taint.Value); v != nil && v.Type != 0 {
		flags := c.Prog.TypeFlags(v.Type)
		if flags&tsbridge.TypeObject != 0 && flags&(tsbridge.TypeString|tsbridge.TypeAny|tsbridge.TypeUnion) == 0 {
			return false
		}
	}
	return built(c)
}
