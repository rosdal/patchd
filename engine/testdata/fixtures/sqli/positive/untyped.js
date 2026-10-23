// Plain JavaScript with require("pg").
const { Pool } = require("pg");
const pg = require("pg");

const pool = new Pool();
const client = new pg.Client();

module.exports.find = function (req, res) {
  pool.query("SELECT * FROM users WHERE id = " + req.params.id, function (err, r) { // expect: ts/sqli
    res.json(r.rows);
  });
};

module.exports.search = async function (req, res) {
  const r = await client.query(`SELECT * FROM items WHERE name LIKE '%${req.query.q}%'`); // expect: ts/sqli
  res.json(r.rows);
};
