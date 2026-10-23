// Safe command execution: constants, argument arrays without a shell,
// numbers and booleans, overwritten values.
import express from "express";
import { exec, execSync, execFile, execFileSync, spawn, spawnSync } from "node:child_process";

const app = express();

app.get("/n1", (req, res) => {
  exec("ls -la"); // expect-none
  res.send(req.query.x); // expect-none
});

app.get("/n2", (req, res) => {
  execFile("ping", ["-c", "1", String(req.query.host)]); // expect-none
  res.end();
});

app.get("/n3", (req, res) => {
  spawn("git", ["log", req.query.ref as string]); // expect-none
  res.end();
});

app.get("/n4", (req, res) => {
  spawn("git", ["log", String(req.query.ref)], { cwd: "/repo" }); // expect-none
  res.end();
});

app.get("/n5", (req, res) => {
  spawn("git", ["log", String(req.query.ref)], { shell: false }); // expect-none
  res.end();
});

app.get("/n6", (req, res) => {
  const n = Number(req.query.n);
  exec("seq " + n); // expect-none
  res.end();
});

app.get("/n7", (req, res) => {
  exec(`head -n ${parseInt(req.query.lines as string, 10)} file.txt`); // expect-none
  res.end();
});

app.get("/n8", (req, res) => {
  exec("echo " + (req.query.flag === "1")); // expect-none
  res.end();
});

app.get("/n9", (req, res) => {
  exec("echo " + typeof req.query.x); // expect-none
  res.end();
});

app.get("/n10", (req, res) => {
  const list = req.query.list as string[];
  exec("echo " + list.length); // expect-none
  res.end();
});

app.get("/n12", (req, res) => {
  exec("ls " + (req.query.x ? "-a" : "-l")); // expect-none
  res.end();
});

app.get("/n13", (req, res) => {
  exec("echo " + process.env.HOME); // expect-none
  exec("node " + process.argv[2]); // expect-none
  res.end();
});

app.get("/n14", (req, res) => {
  console.log(req.query.x);
  exec("date"); // expect-none
  res.end();
});

app.get("/n15", (req, res) => {
  let cmd = req.query.cmd as string;
  cmd = "uptime";
  exec(cmd); // expect-none
  res.end();
});

app.get("/n16", (req, res) => {
  let c = req.query.c as string;
  const g = () => c;
  c = "uptime";
  exec(c); // expect-none
  res.send(g());
});

app.get("/n17", (req, res) => {
  exec(`echo ${Date.now()}`); // expect-none
  res.end();
});

app.get("/n18", (req, res) => {
  spawn("ls", { shell: true }); // expect-none
  res.end();
});

app.get("/n19", (req, res) => {
  execFileSync("convert", [req.query.in as string, "out.png"]); // expect-none
  res.end();
});

app.get("/n20", (req, res) => {
  exec(JSON.stringify(Boolean(req.query.x))); // expect-none
  res.end();
});

app.get("/n21", (req, res) => {
  exec("kill " + Math.floor(Number(req.query.pid))); // expect-none
  res.end();
});

app.get("/n22", (req, res) => {
  exec("echo hi", { cwd: req.query.dir as string }); // expect-none
  res.end();
});

app.get("/n23", (req, res) => {
  const command = "ls";
  execSync(command); // expect-none
  res.json(req.body); // expect-none
});

app.get("/n24", (req, res) => {
  spawnSync("tar", ["xzf", String(req.query.archive)]); // expect-none
  res.end();
});

app.get("/n25", (req, res) => {
  const page = Number.parseInt(String(req.query.page), 10);
  execSync(`./report.sh ${page}`); // expect-none
  res.end();
});

app.get("/n26", (req, res) => {
  const verbose = Boolean(req.query.verbose);
  exec("make " + (verbose ? "V=1" : "")); // expect-none
  res.end();
});

app.get("/n27", (req, res) => {
  const ids = String(req.query.ids).split(",").map(Number);
  exec("echo " + ids.length); // expect-none
  res.end();
});
