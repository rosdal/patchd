// Plain JavaScript with parameterised pg queries.
const { Pool } = require("pg");

const pool = new Pool();

module.exports.find = function (req, res) {
  pool.query("SELECT * FROM users WHERE id = $1", [req.params.id], function (err, r) { // expect-none
    res.json(r.rows);
  });
};

module.exports.count = async function (req, res) {
  const r = await pool.query("SELECT count(*) FROM t WHERE n > " + Number(req.query.n)); // expect-none
  res.json(r.rows);
};

module.exports.config = async function (req, res) {
  await pool.query({ text: "SELECT * FROM t WHERE a = $1", values: [req.query.a] }); // expect-none
  res.end();
};
