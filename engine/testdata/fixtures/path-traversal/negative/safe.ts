// Safe file access: basename, constants, numbers, input in non-path
// arguments, environment variables.
import express from "express";
import * as fs from "node:fs";
import fsp from "node:fs/promises";
import path, { basename } from "node:path";

const app = express();
const BASE = "/srv/files";

app.get("/n1/:file", (req, res) => {
  res.sendFile(path.join(__dirname, "public", path.basename(req.params.file))); // expect-none
});

app.get("/n2", (req, res) => {
  res.send(fs.readFileSync("/srv/" + path.basename(req.query.f as string))); // expect-none
});

app.delete("/n3", (req, res) => {
  const name = path.basename(String(req.query.n));
  fs.unlinkSync(path.join("/tmp", name)); // expect-none
  res.end();
});

app.get("/n4", (req, res) => {
  res.send(fs.readFileSync(basename(String(req.params.x)))); // expect-none
});

app.get("/n5/:id", (req, res) => {
  res.send(fs.readFileSync(`/data/${Number(req.params.id)}.json`)); // expect-none
});

app.get("/n6", (req, res) => {
  res.send(fs.readFileSync(`/data/${parseInt(String(req.query.page), 10)}.html`)); // expect-none
});

app.get("/n7", (req, res) => {
  res.send(fs.readFileSync(process.env.CONFIG_PATH ?? "config.json")); // expect-none
});

app.get("/n8", (req, res) => {
  res.send(fs.readFileSync("./config.json")); // expect-none
  res.send(fs.readFileSync(__filename)); // expect-none
});

app.post("/n9", (req, res) => {
  fs.writeFileSync("out.txt", req.body.content); // expect-none
  res.end();
});

app.post("/n10", (req, res) => {
  fs.appendFileSync("log.txt", req.query.msg + "\n"); // expect-none
  res.end();
});

app.get("/n11", (req, res) => {
  res.sendFile("index.html", { root: "public" }); // expect-none
  res.download("report.pdf"); // expect-none
});

app.get("/n12/:f", (req, res) => {
  res.sendFile(path.join(__dirname, "x", path.basename(decodeURIComponent(req.params.f)))); // expect-none
});

app.post("/n13", (req, res) => {
  fs.createWriteStream("upload.bin").write(req.body.data); // expect-none
  res.end();
});

app.get("/n14", (req, res) => {
  res.send(fs.readFileSync(`/data/${req.query.id === "a" ? "a" : "b"}.json`)); // expect-none
});

app.get("/n15", async (req, res) => {
  const file = req.query.file as string;
  res.send(await fsp.readFile(path.join(BASE, path.basename(file)))); // expect-none
});

app.get("/n16", (req, res) => {
  res.json(fs.existsSync(path.join("/x", path.basename(String(req.query.p))))); // expect-none
});

app.get("/n17", (req, res) => {
  let p = req.query.p as string;
  p = "default.txt";
  res.sendFile(path.join(BASE, p)); // expect-none
});

app.get("/n18", (req, res) => {
  const n = req.query.items ? (req.query.items as string[]).length : 0;
  res.send(fs.readFileSync(`/data/page-${n}.json`)); // expect-none
});

app.get("/n19", (req, res) => {
  res.send(fs.readFileSync(`/logs/${new Date().getFullYear()}.log`)); // expect-none
});

app.get("/n20", (req, res) => {
  const safe = path.basename(req.query.f as string);
  res.download(path.join("reports", safe)); // expect-none
});

app.get("/n21", (req, res) => {
  fs.readFile(path.join(BASE, "index.html"), (err, data) => res.send(data ?? req.query.fallback)); // expect-none
});

app.get("/n22", (req, res) => {
  const ext = req.query.format === "pdf" ? ".pdf" : ".txt";
  res.sendFile(path.join(BASE, "report" + ext)); // expect-none
});

app.get("/n23", (req, res) => {
  const id = Math.trunc(Number(req.query.id));
  res.sendFile(path.join(BASE, `${id}.png`)); // expect-none
});

app.post("/n24", (req, res) => {
  fs.writeFile(path.join(BASE, "latest.json"), JSON.stringify(req.body), () => res.end()); // expect-none
});

app.get("/n25", (req, res) => {
  console.log(req.path);
  res.sendFile(path.join(BASE, "help.html")); // expect-none
});

// A local helper that confines the path: v0.4 doesn't follow values through
// functions in the repo (found in pnpm's e2e server during v0.4 triage).
function confine(p: string): string | undefined {
  const file = path.join(BASE, p);
  return file.startsWith(BASE + path.sep) ? file : undefined;
}

app.get("/n26", (req, res) => {
  const file = confine(String(req.query.p));
  if (file) res.sendFile(file); // expect-none
});

// A lookup table of served files (found in vscode's tests during v0.4
// triage): the request picks an entry but can't write one.
const SERVED: Record<string, string> = { "logo.png": "/srv/static/logo.png" };

app.get("/n27", (req, res) => {
  const file = SERVED[req.path.slice(1)];
  if (file) fs.createReadStream(file).pipe(res); // expect-none
});
