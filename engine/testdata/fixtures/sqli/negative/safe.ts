// Safe queries: parameters, query configs, numbers, constants.
import express from "express";
import { Pool, Client } from "pg";

const app = express();
const pool = new Pool();

app.get("/n1", async (req, res) => {
  const result = await pool.query("SELECT * FROM users WHERE id = $1", [req.query.id]); // expect-none
  res.json(result.rows);
});

app.get("/n2", async (req, res) => {
  await pool.query({ text: "SELECT * FROM t WHERE a = $1", values: [req.query.a] }); // expect-none
  res.end();
});

app.get("/n3", async (req, res) => {
  const config = { text: "INSERT INTO t (a, b) VALUES ($1, $2)", values: [req.body.a, req.body.b] };
  await pool.query(config); // expect-none
  res.end();
});

app.get("/n4/:id", async (req, res) => {
  await pool.query(`SELECT * FROM t WHERE id = ${Number(req.params.id)}`); // expect-none
  res.end();
});

app.get("/n5", async (req, res) => {
  await pool.query("SELECT * FROM t LIMIT " + parseInt(String(req.query.limit), 10)); // expect-none
  res.end();
});

app.get("/n6", async (req, res) => {
  await pool.query(`SELECT * FROM t LIMIT ${req.query.limit === "all" ? 1000 : 10}`); // expect-none
  res.end();
});

app.get("/n7", async (req, res) => {
  await pool.query("SET search_path TO " + process.env.SCHEMA); // expect-none
  res.end();
});

app.get("/n8", async (req, res) => {
  const client = new Client();
  await client.query("SELECT 1"); // expect-none
  res.json(req.query); // expect-none
});

app.post("/n9", async (req, res) => {
  await pool.query("INSERT INTO t (a, b) VALUES ($1, $2)", [req.body.a, req.body.b]); // expect-none
  res.end();
});

app.get("/n10", async (req, res) => {
  const id = Number(req.params.id);
  await pool.query("SELECT * FROM t WHERE id = " + id); // expect-none
  res.end();
});

app.get("/n11", async (req, res) => {
  const limit = Math.min(Number(req.query.limit), 100);
  await pool.query(`SELECT * FROM t LIMIT ${limit}`); // expect-none
  res.end();
});

app.post("/n12", async (req, res) => {
  const ids = req.body.ids as string[];
  await pool.query(`SELECT * FROM t LIMIT ${ids.length}`); // expect-none
  res.end();
});

app.get("/n13", async (req, res) => {
  const sort = req.query.desc === "1" ? "DESC" : "ASC";
  await pool.query(`SELECT * FROM t ORDER BY created ${sort}`); // expect-none
  res.end();
});

app.get("/n14", async (req, res) => {
  let sql = req.query.sql as string;
  sql = "SELECT now()";
  await pool.query(sql); // expect-none
  res.end();
});

app.get("/n15", async (req, res) => {
  const values = [req.query.a, req.query.b];
  await pool.query("SELECT * FROM t WHERE a = $1 AND b = $2", values); // expect-none
  res.end();
});

app.get("/n16", async (req, res) => {
  const page = Math.max(0, Number.parseInt(String(req.query.page), 10));
  await pool.query("SELECT * FROM t OFFSET " + page * 20); // expect-none
  res.end();
});

app.get("/n17", async (req, res) => {
  const active = Boolean(req.query.active);
  await pool.query(`SELECT * FROM users WHERE active = ${active}`); // expect-none
  res.end();
});

app.get("/n18", async (req, res) => {
  const c = await pool.connect();
  await c.query("SELECT * FROM t WHERE id = $1", [req.params.id]); // expect-none
  c.release();
  res.end();
});

app.get("/n19", async (req, res) => {
  await pool.query(`SELECT * FROM t WHERE ts > ${Date.now() - 3600}`); // expect-none
  res.end();
});

app.get("/n20", async (req, res) => {
  const r = await pool.query("SELECT * FROM t");
  res.json(r.rows.filter((row) => row.name === req.query.name)); // expect-none
});

app.get("/n21", async (req, res) => {
  const year = Math.trunc(Number(req.query.year));
  await pool.query(`SELECT * FROM sales WHERE year = ${year}`); // expect-none
  res.end();
});

app.get("/n22", async (req, res) => {
  await pool.query({ text: `SELECT * FROM t WHERE a = $1`, values: [String(req.query.a).trim()] }); // expect-none
  res.end();
});

app.get("/n23", async (req, res) => {
  const count = (req.query.tags as string[] | undefined)?.length ?? 0;
  await pool.query("SELECT * FROM t LIMIT " + count); // expect-none
  res.end();
});

app.get("/n24", async (req, res) => {
  console.log(req.query.debug);
  await pool.query("SELECT version()"); // expect-none
  res.end();
});

app.get("/n25", async (req, res) => {
  const order = req.query.newest !== undefined ? "created DESC" : "created";
  await pool.query("SELECT * FROM t ORDER BY " + order); // expect-none
  res.end();
});
