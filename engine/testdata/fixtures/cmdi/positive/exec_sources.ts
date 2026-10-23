// Every Express source reaching exec or execSync directly.
import express from "express";
import { exec, execSync } from "node:child_process";

const app = express();

app.get("/ping", (req, res) => {
  const host = req.query.host as string;
  exec(`ping -c 1 ${host}`); // expect: ts/cmdi from=8
  res.send("ok");
});

app.get("/ls", (req, res) => {
  exec("ls " + req.query.dir); // expect: ts/cmdi
  res.end();
});

app.post("/run", (req, res) => {
  execSync(req.body.command); // expect: ts/cmdi
  res.end();
});

app.get("/cat/:name", (req, res) => {
  execSync("cat /tmp/" + req.params.name); // expect: ts/cmdi
  res.end();
});

app.get("/forwarded", (req, res) => {
  exec("echo " + req.headers["x-forwarded-for"]); // expect: ts/cmdi
  res.end();
});

app.get("/agent", (req, res) => {
  exec("echo " + req.get("user-agent")); // expect: ts/cmdi
  res.end();
});

app.get("/header", (req, res) => {
  exec("echo " + req.header("x-trace")); // expect: ts/cmdi
  res.end();
});

app.get("/session", (req, res) => {
  exec(`grep ${req.cookies.session} /var/log/app.log`); // expect: ts/cmdi
  res.end();
});

app.get("/path", (req, res) => {
  exec("logger " + req.path); // expect: ts/cmdi
  res.end();
});

app.get("/url", (req, res) => {
  exec("logger " + req.originalUrl); // expect: ts/cmdi
  res.end();
});

app.get("/host", (req, res) => {
  exec("nslookup " + req.hostname); // expect: ts/cmdi
  res.end();
});
