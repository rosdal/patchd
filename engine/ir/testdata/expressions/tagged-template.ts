declare function sql(strings: TemplateStringsArray, ...values: unknown[]): string;
function f(id: string) {
  return sql`SELECT * FROM users WHERE id = ${id}`;
}
