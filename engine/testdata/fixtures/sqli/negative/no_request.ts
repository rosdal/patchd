// Queries built from values that are not request input.
import { Pool } from "pg";

const pool = new Pool();

export async function migrate(table: string) {
  await pool.query("CREATE TABLE IF NOT EXISTS " + table + " (id serial)"); // expect-none
}

export async function byId(id: string) {
  return pool.query(`SELECT * FROM t WHERE id = '${id}'`); // expect-none
}

const TABLES = ["users", "orders"];
export async function truncateAll() {
  for (const t of TABLES) {
    await pool.query("TRUNCATE " + t); // expect-none
  }
}

export async function stats(days: number) {
  await pool.query(`SELECT count(*) FROM visits WHERE day > now() - interval '${days} days'`); // expect-none
}

export async function health() {
  await pool.query("SELECT 1"); // expect-none
}
