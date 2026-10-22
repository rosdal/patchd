declare const Prisma: { sql(s: TemplateStringsArray, ...v: unknown[]): unknown };
function f(name: string) {
  return Prisma.sql`SELECT * FROM t WHERE name = ${name}`;
}
