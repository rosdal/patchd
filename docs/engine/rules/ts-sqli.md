# ts/sqli: SQL injection

**CWE-89 · critical · on by default**

Reports SQL query text built from user input in the same function and run with `pg` (`Client.query`, `Pool.query`, `PoolClient.query`, argument 0). The tainted part must be concatenated (`+`, `+=`), interpolated in a template literal, or put in by a string-building call (`concat`, `join`, `replace`, `format`).

```ts
app.get("/users/:id", async (req, res) => {
  await pool.query(`SELECT * FROM users WHERE id = ${req.params.id}`); // ts/sqli
});
```

## Fix

Send input as parameters:

```ts
await pool.query("SELECT * FROM users WHERE id = $1", [req.params.id]);
```

Table and column names can't be parameters; map them from a fixed allowlist.

## What is not reported

- Values in the parameter array, and `{ text, values }` query configs.
- Numbers and booleans: `` `... LIMIT ${Number(req.query.limit)}` ``.
- A query that is entirely user input (`pool.query(req.body.sql)`), which is not "built": the rule reports injection into query text, and v0.4 keeps it to that shape.

## Known false positives

- Validation in a branch (`if (!SORTS.includes(sort)) return`) still reports the query (v0.5).
- One tainted field taints the whole object: `` `SELECT * FROM ${opts.table}` `` where only `opts.id` came from the request (v0.5).

## Known misses

- The query is built in a helper or repository class that receives the input as a parameter (v0.5).
- Other database libraries: Sequelize (`sequelize.query`, which DVNA and Juice Shop use), Knex, Prisma `$queryRawUnsafe`, `mysql2` (v0.6).

## Precision at v0.4

No findings on the eight non-vulnerable reference repos or on Patchd. The SQL injections in DVNA and Juice Shop go through Sequelize and are expected misses until v0.6. See [eval/reports/v0.4-reference.md](../../../eval/reports/v0.4-reference.md).
