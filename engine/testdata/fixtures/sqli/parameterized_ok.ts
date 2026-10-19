// expect-none
import express from "express";
import { Pool } from "pg";

const app = express();
const pool = new Pool();

app.get("/users", async (req, res) => {
  const result = await pool.query("SELECT * FROM users WHERE id = $1", [req.query.id]);
  res.json(result.rows);
});
