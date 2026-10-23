// Query text built from request input and run with pg.
import express, { type Request, type Response } from "express";
import { Pool, Client } from "pg";
import { format } from "node:util";

const app = express();
const pool = new Pool();

app.get("/users/:id", async (req, res) => {
  const result = await pool.query(`SELECT * FROM users WHERE id = ${req.params.id}`); // expect: ts/sqli
  res.json(result.rows);
});

app.get("/users", async (req, res) => {
  const id = req.query.id;
  const result = await pool.query(`SELECT * FROM users WHERE id = ${id}`); // expect: ts/sqli from=15
  res.json(result.rows);
});

app.post("/login", async (req, res) => {
  const q = "SELECT * FROM users WHERE name = '" + req.body.name + "' AND pass = '" + req.body.pass + "'";
  const result = await pool.query(q); // expect: ts/sqli
  res.json(result.rows);
});

app.get("/search", async (req, res) => {
  let sql = "SELECT * FROM products";
  sql += " WHERE name LIKE '%" + req.query.term + "%'";
  res.json((await pool.query(sql)).rows); // expect: ts/sqli
});

app.get("/sorted", async (req, res) => {
  res.json((await pool.query(`SELECT * FROM items ORDER BY ${req.query.sort} DESC`)).rows); // expect: ts/sqli
});

app.get("/table", async (req, res) => {
  // Parameters for values don't help when the table name is text.
  const r = await pool.query("SELECT * FROM " + req.query.table + " WHERE id = $1", [1]); // expect: ts/sqli
  res.json(r.rows);
});

app.get("/client", async (req, res) => {
  const client = new Client();
  await client.connect();
  const r = await client.query(`DELETE FROM sessions WHERE token = '${req.cookies.token}'`); // expect: ts/sqli
  res.json(r.rowCount);
});

app.get("/pool-client", async (req, res) => {
  const c = await pool.connect();
  try {
    await c.query("UPDATE users SET seen = now() WHERE email = '" + req.query.email + "'"); // expect: ts/sqli
  } finally {
    c.release();
  }
  res.end();
});

app.post("/in-list", async (req, res) => {
  const ids: string[] = req.body.ids;
  await pool.query("SELECT * FROM t WHERE id IN (" + ids.join(",") + ")"); // expect: ts/sqli
  res.end();
});

app.get("/format", async (req, res) => {
  await pool.query(format("SELECT * FROM t WHERE a = '%s'", req.query.a)); // expect: ts/sqli
  res.end();
});

app.get("/replace", async (req, res) => {
  const tpl = "SELECT * FROM t WHERE id = {id}";
  await pool.query(tpl.replace("{id}", String(req.params.id))); // expect: ts/sqli
  res.end();
});

app.get("/concat", async (req, res) => {
  await pool.query("SELECT * FROM logs WHERE level = ".concat(String(req.query.level))); // expect: ts/sqli
  res.end();
});

app.get("/branches", async (req, res) => {
  let where = "";
  if (req.query.name) {
    where = " WHERE name = '" + req.query.name + "'";
  }
  await pool.query("SELECT * FROM t" + where); // expect: ts/sqli
  res.end();
});

app.get("/loop", async (req, res) => {
  let sql = "SELECT * FROM t WHERE 1=1";
  for (const [k, v] of Object.entries(req.query)) {
    sql += ` AND ${k} = '${v}'`;
  }
  await pool.query(sql); // expect: ts/sqli
  res.end();
});

app.get("/destructure", async (req, res) => {
  const { user, order } = req.query;
  await pool.query(`SELECT * FROM orders WHERE user_id = '${user}' ORDER BY ${order}`); // expect: ts/sqli
  res.end();
});

app.get("/try", async (req, res) => {
  try {
    const r = await pool.query("SELECT * FROM t WHERE x = '" + req.query.x + "'"); // expect: ts/sqli
    res.json(r.rows);
  } catch {
    res.sendStatus(500);
  }
});

app.get("/header", async (req, res) => {
  await pool.query(`INSERT INTO visits (agent) VALUES ('${req.get("user-agent")}')`); // expect: ts/sqli
  res.end();
});

app.get("/callback", (req, res) => {
  pool.query("SELECT * FROM t WHERE id = " + req.params.id, (err, r) => res.json(r.rows)); // expect: ts/sqli
});

export async function report(req: Request, res: Response) {
  const from = req.query.from;
  const to = req.query.to;
  const sql = `SELECT * FROM sales WHERE day BETWEEN '${from}' AND '${to}'`;
  res.json((await pool.query(sql)).rows); // expect: ts/sqli
}

export class UserRepo {
  async find(req: Request) {
    return pool.query(`SELECT * FROM users WHERE name = '${req.params.name}'`); // expect: ts/sqli
  }
}

app.get("/captured", async (req, res) => {
  const name = req.query.name as string;
  const log = () => name;
  await pool.query("SELECT * FROM t WHERE name = '" + name + "'"); // expect: ts/sqli
  res.send(log());
});

app.get("/upper", async (req, res) => {
  const code = String(req.query.code).trim().toUpperCase();
  await pool.query(`SELECT * FROM countries WHERE code = '${code}'`); // expect: ts/sqli
  res.end();
});

app.get("/nested", async (req, res) => {
  const filter = { column: req.query.column };
  await pool.query(`SELECT ${filter.column} FROM t`); // expect: ts/sqli
  res.end();
});

app.get("/json", async (req, res) => {
  const body = JSON.parse(String(req.query.q));
  await pool.query("SELECT * FROM t WHERE tag = '" + body.tag + "'"); // expect: ts/sqli
  res.end();
});

app.get("/ternary", async (req, res) => {
  const col = req.query.col ? req.query.col : "id";
  await pool.query(`SELECT * FROM t ORDER BY ${col}`); // expect: ts/sqli
  res.end();
});

app.get("/await-input", async (req, res) => {
  const name = await Promise.resolve(req.query.name);
  await pool.query("SELECT * FROM t WHERE name = '" + name + "'"); // expect: ts/sqli
  res.end();
});

app.get("/annotated", async (req, res) => {
  // A number annotation validates nothing at runtime.
  const id: number = req.body.id;
  await pool.query("SELECT * FROM t WHERE id = " + id); // expect: ts/sqli
  res.end();
});

app.get("/switch", async (req, res) => {
  switch (req.query.kind) {
    case "name":
      await pool.query("SELECT * FROM t WHERE name = '" + req.query.value + "'"); // expect: ts/sqli
      break;
    default:
      res.sendStatus(400);
  }
});

app.get("/while", async (req, res) => {
  const words = String(req.query.words).split(" ");
  let cond = "1=1";
  let i = 0;
  while (i < words.length) {
    cond = cond + ` OR body LIKE '%${words[i]}%'`;
    i++;
  }
  await pool.query("SELECT * FROM posts WHERE " + cond); // expect: ts/sqli
  res.end();
});

app.get("/path", async (req, res) => {
  await pool.query(`INSERT INTO hits (path) VALUES ('${req.originalUrl}')`); // expect: ts/sqli
  res.end();
});
