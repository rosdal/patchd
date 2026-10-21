// Generates engine/tsbridge/kind_gen.go: the Patchd Kind enum and the table that
// maps every syntax kind of the pinned typescript-go to a Patchd kind. Compiler
// kinds without a Patchd equivalent map to a group kind (TypeNode, Modifier,
// Token, JSDoc) or to KindOther, never to nothing: kind_test.go fails when the
// compiler has a kind this table doesn't list, so upgrades surface new syntax.
//
// Usage: node tools/scripts/gen-kinds.mjs (part of `pnpm gen`).
import { execFileSync } from "node:child_process";
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { repoRoot } from "./lib/targets.mjs";

// Patchd kinds, in enum order. Append only: later layers may persist kind values.
// A name that matches a compiler kind maps 1:1 unless `from` lists other sources.
const patchdKinds = [
  { name: "Invalid", from: [] }, // the kind of a zero NodeID
  { name: "Other", from: [] }, // compiler syntax Patchd does not model
  { name: "Token", from: [] }, // punctuation, operators and keywords that carry no meaning of their own
  { name: "Modifier", from: [] }, // export, async, static, readonly, …
  { name: "TypeNode", from: [] }, // any type annotation or type-level syntax, kept opaque
  { name: "JSDoc", from: [] }, // JSDoc comments and tags

  // Files and modules
  "SourceFile",
  "ImportDeclaration",
  "ImportClause",
  "NamespaceImport",
  "NamedImports",
  "ImportSpecifier",
  "ImportEqualsDeclaration",
  "ExternalModuleReference",
  "ImportAttributes",
  "ImportAttribute",
  "ExportDeclaration",
  "NamedExports",
  "NamespaceExport",
  "ExportSpecifier",
  "ExportAssignment",
  "NamespaceExportDeclaration",
  "ModuleDeclaration",
  "ModuleBlock",

  // Declarations
  "VariableStatement",
  "VariableDeclarationList",
  "VariableDeclaration",
  "FunctionDeclaration",
  "ClassDeclaration",
  "InterfaceDeclaration",
  "TypeAliasDeclaration",
  "EnumDeclaration",
  "EnumMember",
  "Parameter",
  "TypeParameter",
  "Decorator",
  "PropertyDeclaration",
  "MethodDeclaration",
  "Constructor",
  "GetAccessor",
  "SetAccessor",
  "ClassStaticBlockDeclaration",
  "SemicolonClassElement",
  "HeritageClause",
  "PropertySignature",
  "MethodSignature",
  "ObjectBindingPattern",
  "ArrayBindingPattern",
  "BindingElement",
  "ComputedPropertyName",
  "QualifiedName",

  // Statements
  "Block",
  "EmptyStatement",
  "ExpressionStatement",
  "IfStatement",
  "DoStatement",
  "WhileStatement",
  "ForStatement",
  "ForInStatement",
  "ForOfStatement",
  "ContinueStatement",
  "BreakStatement",
  "ReturnStatement",
  "WithStatement",
  "SwitchStatement",
  "CaseBlock",
  "CaseClause",
  "DefaultClause",
  "LabeledStatement",
  "ThrowStatement",
  "TryStatement",
  "CatchClause",
  "DebuggerStatement",

  // Expressions
  "Identifier",
  "PrivateIdentifier",
  "NumericLiteral",
  "BigIntLiteral",
  "StringLiteral",
  "RegularExpressionLiteral",
  "NoSubstitutionTemplateLiteral",
  "TemplateExpression",
  "TemplateHead",
  "TemplateMiddle",
  "TemplateTail",
  "TemplateSpan",
  "TaggedTemplateExpression",
  "ArrayLiteralExpression",
  "ObjectLiteralExpression",
  "PropertyAssignment",
  "ShorthandPropertyAssignment",
  "SpreadAssignment",
  "PropertyAccessExpression",
  "ElementAccessExpression",
  "CallExpression",
  "NewExpression",
  "ParenthesizedExpression",
  "FunctionExpression",
  "ArrowFunction",
  "DeleteExpression",
  "TypeOfExpression",
  "VoidExpression",
  "AwaitExpression",
  "PrefixUnaryExpression",
  "PostfixUnaryExpression",
  "BinaryExpression",
  "ConditionalExpression",
  "YieldExpression",
  "SpreadElement",
  "ClassExpression",
  "OmittedExpression",
  "ExpressionWithTypeArguments",
  "AsExpression",
  "SatisfiesExpression",
  "NonNullExpression",
  "TypeAssertionExpression",
  "MetaProperty",
  { name: "This", from: ["ThisKeyword"] },
  { name: "Super", from: ["SuperKeyword"] },
  { name: "Null", from: ["NullKeyword"] },
  { name: "True", from: ["TrueKeyword"] },
  { name: "False", from: ["FalseKeyword"] },
  { name: "ImportKeyword", from: ["ImportKeyword"] }, // callee of import(…), object of import.meta

  // JSX
  "JsxElement",
  "JsxSelfClosingElement",
  "JsxOpeningElement",
  "JsxClosingElement",
  "JsxFragment",
  "JsxOpeningFragment",
  "JsxClosingFragment",
  "JsxAttribute",
  "JsxAttributes",
  "JsxSpreadAttribute",
  "JsxExpression",
  { name: "JsxText", from: ["JsxText", "JsxTextAllWhiteSpaces"] },
  "JsxNamespacedName",
];

const modifierKeywords = new Set(
  [
    "Abstract",
    "Accessor",
    "Async",
    "Const",
    "Declare",
    "Default",
    "Export",
    "Override",
    "Private",
    "Protected",
    "Public",
    "Readonly",
    "Static",
  ].map((k) => `${k}Keyword`),
);
const typeKeywords = new Set(
  [
    "Any",
    "BigInt",
    "Boolean",
    "Intrinsic",
    "Never",
    "Number",
    "Object",
    "String",
    "Symbol",
    "Undefined",
    "Unknown",
    "Void",
  ].map((k) => `${k}Keyword`),
);
const typeMembers = new Set(["CallSignature", "ConstructSignature", "IndexSignature"]);

const compilerKinds = JSON.parse(
  execFileSync("go", ["run", "./engine/tsbridge/internal/kinddump"], {
    cwd: repoRoot,
    encoding: "utf8",
  }),
);

const names = patchdKinds.map((k) => (typeof k === "string" ? k : k.name));
const explicit = new Map();
for (const k of patchdKinds) {
  for (const from of typeof k === "string" ? [k] : k.from)
    explicit.set(from, typeof k === "string" ? k : k.name);
}

function mapKind({ name, category }) {
  if (explicit.has(name)) return explicit.get(name);
  if (category === "jsdoc" || name === "JSDocCommentTextToken") return "JSDoc";
  if (category === "type" || typeKeywords.has(name) || typeMembers.has(name)) return "TypeNode";
  if (modifierKeywords.has(name)) return "Modifier";
  if (["keyword", "punctuation", "trivia", "token"].includes(category)) return "Token";
  return "Other";
}

const compilerNames = new Set(compilerKinds.map((k) => k.name));
const stale = [...explicit.keys()].filter((n) => !compilerNames.has(n));
if (stale.length > 0)
  throw new Error(`Patchd kinds map from compiler kinds that no longer exist: ${stale.join(", ")}`);

const lines = [
  "// Code generated by tools/scripts/gen-kinds.mjs; DO NOT EDIT.",
  "",
  "package tsbridge",
  "",
  'import "github.com/microsoft/typescript-go/shim/ast"',
  "",
  "// Patchd syntax kinds. See kind.go.",
  "const (",
  ...names.map((n, i) => `\tKind${n}${i === 0 ? " Kind = iota" : ""}`),
  ")",
  "",
  "var kindNames = [...]string{",
  ...names.map((n) => `\tKind${n}: ${JSON.stringify(n)},`),
  "}",
  "",
  "// kindTable maps every compiler kind of the pinned typescript-go to a Patchd kind.",
  "var kindTable = [ast.KindCount]Kind{",
  ...compilerKinds.map((k) => `\tast.Kind${k.name}: Kind${mapKind(k)},`),
  "}",
  "",
  "// compilerKindCount is ast.KindCount when this file was generated.",
  `const compilerKindCount = ${compilerKinds.length}`,
  "",
];
const out = join(repoRoot, "engine", "tsbridge", "kind_gen.go");
writeFileSync(out, lines.join("\n"));
execFileSync("gofmt", ["-w", out], { stdio: "inherit" });
const counts = {};
for (const k of compilerKinds) counts[mapKind(k)] = (counts[mapKind(k)] ?? 0) + 1;
console.log(
  `gen-kinds: ${compilerKinds.length} compiler kinds -> ${names.length} Patchd kinds (Other: ${counts.Other ?? 0})`,
);
