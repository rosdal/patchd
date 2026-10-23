// A raw Node http server logging its requests with pg.
import http from "node:http";
import { Pool } from "pg";

const pool = new Pool();

http
  .createServer(async (req, res) => {
    await pool.query("INSERT INTO log (url) VALUES ('" + req.url + "')"); // expect: ts/sqli
    res.end();
  })
  .listen(8081);
