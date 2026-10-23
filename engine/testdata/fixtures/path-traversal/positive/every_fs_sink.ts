// One positive case per built-in fs and fs/promises sink (Step 4: every model
// entry has a fixture). Each call passes the request value as its first path.
import express from "express";
import * as fs from "node:fs";
import fsp from "node:fs/promises";

const app = express();

app.get("/every-fs-sink", async (req, res) => {
  const p = String(req.query.p);
  fs.readFile(p, () => {}); // expect: ts/path-traversal
  fs.readFileSync(p); // expect: ts/path-traversal
  await fsp.readFile(p); // expect: ts/path-traversal
  fs.writeFile(p, "x", () => {}); // expect: ts/path-traversal
  fs.writeFileSync(p, "x"); // expect: ts/path-traversal
  await fsp.writeFile(p, "x"); // expect: ts/path-traversal
  fs.appendFile(p, "x", () => {}); // expect: ts/path-traversal
  fs.appendFileSync(p, "x"); // expect: ts/path-traversal
  await fsp.appendFile(p, "x"); // expect: ts/path-traversal
  fs.stat(p, () => {}); // expect: ts/path-traversal
  fs.statSync(p); // expect: ts/path-traversal
  await fsp.stat(p); // expect: ts/path-traversal
  fs.lstat(p, () => {}); // expect: ts/path-traversal
  fs.lstatSync(p); // expect: ts/path-traversal
  await fsp.lstat(p); // expect: ts/path-traversal
  fs.unlink(p, () => {}); // expect: ts/path-traversal
  fs.unlinkSync(p); // expect: ts/path-traversal
  await fsp.unlink(p); // expect: ts/path-traversal
  fs.readdir(p, () => {}); // expect: ts/path-traversal
  fs.readdirSync(p); // expect: ts/path-traversal
  await fsp.readdir(p); // expect: ts/path-traversal
  fs.open(p, "r", () => {}); // expect: ts/path-traversal
  fs.openSync(p, "r"); // expect: ts/path-traversal
  await fsp.open(p, "r"); // expect: ts/path-traversal
  fs.access(p, () => {}); // expect: ts/path-traversal
  fs.accessSync(p); // expect: ts/path-traversal
  await fsp.access(p); // expect: ts/path-traversal
  fs.rm(p, () => {}); // expect: ts/path-traversal
  fs.rmSync(p); // expect: ts/path-traversal
  await fsp.rm(p); // expect: ts/path-traversal
  fs.rmdir(p, () => {}); // expect: ts/path-traversal
  fs.rmdirSync(p); // expect: ts/path-traversal
  await fsp.rmdir(p); // expect: ts/path-traversal
  fs.mkdir(p, () => {}); // expect: ts/path-traversal
  fs.mkdirSync(p); // expect: ts/path-traversal
  await fsp.mkdir(p); // expect: ts/path-traversal
  fs.readlink(p, () => {}); // expect: ts/path-traversal
  fs.readlinkSync(p); // expect: ts/path-traversal
  await fsp.readlink(p); // expect: ts/path-traversal
  fs.realpath(p, () => {}); // expect: ts/path-traversal
  fs.realpathSync(p); // expect: ts/path-traversal
  await fsp.realpath(p); // expect: ts/path-traversal
  fs.truncate(p, () => {}); // expect: ts/path-traversal
  fs.truncateSync(p); // expect: ts/path-traversal
  await fsp.truncate(p); // expect: ts/path-traversal
  fs.chmod(p, 0o644, () => {}); // expect: ts/path-traversal
  fs.chmodSync(p, 0o644); // expect: ts/path-traversal
  await fsp.chmod(p, 0o644); // expect: ts/path-traversal
  fs.chown(p, 1, 1, () => {}); // expect: ts/path-traversal
  fs.chownSync(p, 1, 1); // expect: ts/path-traversal
  await fsp.chown(p, 1, 1); // expect: ts/path-traversal
  fs.rename(p, "x", () => {}); // expect: ts/path-traversal
  fs.renameSync(p, "x"); // expect: ts/path-traversal
  await fsp.rename(p, "x"); // expect: ts/path-traversal
  fs.copyFile(p, "x", () => {}); // expect: ts/path-traversal
  fs.copyFileSync(p, "x"); // expect: ts/path-traversal
  await fsp.copyFile(p, "x"); // expect: ts/path-traversal
  fs.cp(p, "x", () => {}); // expect: ts/path-traversal
  fs.cpSync(p, "x"); // expect: ts/path-traversal
  await fsp.cp(p, "x"); // expect: ts/path-traversal
  fs.symlink(p, "x", () => {}); // expect: ts/path-traversal
  fs.symlinkSync(p, "x"); // expect: ts/path-traversal
  await fsp.symlink(p, "x"); // expect: ts/path-traversal
  fs.link(p, "x", () => {}); // expect: ts/path-traversal
  fs.linkSync(p, "x"); // expect: ts/path-traversal
  await fsp.link(p, "x"); // expect: ts/path-traversal
  fs.exists(p, () => {}); // expect: ts/path-traversal
  res.end();
});
