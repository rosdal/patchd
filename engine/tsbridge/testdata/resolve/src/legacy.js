const { exec } = require("child_process");
const db = require("./db-cjs.js");
const { Client } = require("pg");

exec("ls"); // resolves-to: node:child_process.exec
db.connect(); // resolves-to: local:src/db-cjs.js#connect
new Client(); // resolves-to: pg:Client
