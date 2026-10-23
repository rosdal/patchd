// Request input reaching fs and fs/promises path arguments.
import express from "express";
import * as fs from "node:fs";
import fsp from "node:fs/promises";
import { readFile, writeFile, unlink } from "node:fs/promises";
import path from "node:path";

const app = express();
const BASE = "/srv/files";

app.get("/read", (req, res) => {
  const data = fs.readFileSync(req.query.file as string); // expect: ts/path-traversal
  res.send(data);
});

app.get("/read-cb/:name", (req, res) => {
  fs.readFile(path.join(BASE, req.params.name), (err, data) => res.send(data)); // expect: ts/path-traversal
});

app.get("/stream/:f", (req, res) => {
  fs.createReadStream("uploads/" + req.params.f).pipe(res); // expect: ts/path-traversal
});

app.post("/write", (req, res) => {
  fs.writeFileSync(req.body.path, "data"); // expect: ts/path-traversal
  res.end();
});

app.delete("/tmp", (req, res) => {
  fs.unlinkSync(`/tmp/${req.query.name}`); // expect: ts/path-traversal
  res.end();
});

app.get("/stat", (req, res) => {
  res.json(fs.statSync(path.resolve(BASE, String(req.query.p)))); // expect: ts/path-traversal
});

app.get("/exists", (req, res) => {
  res.json(fs.existsSync(BASE + "/" + req.query.p)); // expect: ts/path-traversal
});

app.get("/promises", async (req, res) => {
  res.send(await fsp.readFile(path.join(BASE, String(req.query.f)), "utf8")); // expect: ts/path-traversal
});

app.post("/save", async (req, res) => {
  await writeFile(`${BASE}/${req.body.name}`, req.body.content); // expect: ts/path-traversal
  res.end();
});

app.delete("/remove/:id", async (req, res) => {
  await unlink(path.join(BASE, req.params.id)); // expect: ts/path-traversal
  res.end();
});

app.get("/named-read", async (req, res) => {
  const text = await readFile(String(req.query.config)); // expect: ts/path-traversal
  res.send(text);
});

app.post("/rename", (req, res) => {
  fs.renameSync(req.body.from, BASE + "/archive"); // expect: ts/path-traversal
  res.end();
});

app.post("/copy", (req, res) => {
  fs.copyFileSync(BASE + "/template", req.body.to); // expect: ts/path-traversal
  res.end();
});

app.post("/append", (req, res) => {
  fs.appendFile(req.body.log, "entry\n", () => res.end()); // expect: ts/path-traversal
});

app.get("/list", (req, res) => {
  res.json(fs.readdirSync(path.join(BASE, String(req.query.dir)))); // expect: ts/path-traversal
});

app.get("/access", (req, res) => {
  fs.accessSync(String(req.query.p)); // expect: ts/path-traversal
  res.end();
});

app.post("/mkdir", (req, res) => {
  fs.mkdirSync(path.join(BASE, req.body.dir), { recursive: true }); // expect: ts/path-traversal
  res.end();
});

app.delete("/rm", (req, res) => {
  fs.rmSync(BASE + "/" + req.query.dir, { recursive: true }); // expect: ts/path-traversal
  res.end();
});

app.post("/upload", (req, res) => {
  const out = fs.createWriteStream(path.join("uploads", req.body.filename)); // expect: ts/path-traversal
  req.pipe(out);
});

app.get("/lstat", async (req, res) => {
  res.json(await fsp.lstat(String(req.query.p))); // expect: ts/path-traversal
});

app.get("/open", (req, res) => {
  const fd = fs.openSync(path.join(BASE, String(req.query.f)), "r"); // expect: ts/path-traversal
  fs.closeSync(fd);
  res.end();
});
