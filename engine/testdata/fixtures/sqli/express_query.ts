import express from "express";
import { Pool } from "pg";

const app = express();
const pool = new Pool();

app.get("/users", async (req, res) => {
  const id = req.query.id;
  // expect: sql-injection
  const result = await pool.query(`SELECT * FROM users WHERE id = ${id}`);
  res.json(result.rows);
});
