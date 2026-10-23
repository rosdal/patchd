package security

import (
	"github.com/patchd/patchd/engine/models"
	"github.com/patchd/patchd/engine/rules"
	"github.com/patchd/patchd/engine/taint"
)

func init() {
	rules.Register(&rules.Rule{
		ID:       models.RulePathTraversal,
		Name:     "Path traversal",
		CWE:      []string{"CWE-22"},
		Severity: rules.High,
		Kind:     taint.KindPath,
		Short:    "User input is used as a file system path.",
		Long: "User-controlled data reaches a file system call (`fs.readFile`, `fs.createReadStream`, `fs.unlink`, …) " +
			"or Express's `res.sendFile`/`res.download` as the path. With `../` segments or an absolute path an " +
			"attacker can read, overwrite or delete files outside the directory the code meant to serve.\n\n" +
			"A value that passed through `path.basename` is not reported. `path.join` and `path.resolve` don't " +
			"make a path safe: `path.join(\"/srv/files\", \"../../etc/passwd\")` leaves the directory.",
		Fix: "Reduce the input to a file name, or resolve it and check it stays inside the base directory:\n\n" +
			"```ts\n" +
			"// Unsafe\n" +
			"res.sendFile(path.join(UPLOADS, req.params.name));\n\n" +
			"// Safe: only a file name, no directories\n" +
			"res.sendFile(path.join(UPLOADS, path.basename(req.params.name)));\n\n" +
			"// Safe: resolved and checked\n" +
			"const file = path.resolve(UPLOADS, req.params.name);\n" +
			"if (!file.startsWith(UPLOADS + path.sep)) return res.sendStatus(403);\n" +
			"res.sendFile(file);\n" +
			"```\n\n" +
			"v0.4 still reports the second safe form: the check is a branch, which needs path-sensitive analysis " +
			"(see the rule's known limitations).",
		Advice: "without being confined to a base directory",
		// Off by default in v0.4: below the 80% precision gate on the
		// reference repos (eval/triage/v0.4.yaml). patchd scan --rules
		// ts/path-traversal turns it on.
		Disabled: true,
	})
}
