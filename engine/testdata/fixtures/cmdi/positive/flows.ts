// Taint through local variables, control flow and string operations.
import express, { type Request, type Response } from "express";
import { exec, execSync, spawn, spawnSync, execFile } from "node:child_process";
import * as cp from "node:child_process";

const router = express.Router();

router.get("/compound", (req, res) => {
  let cmd = "convert ";
  cmd += req.query.file;
  cmd += " out.png";
  exec(cmd); // expect: ts/cmdi from=10
  res.end();
});

router.get("/branches", (req, res) => {
  let cmd: string;
  if (req.query.mode === "fast") {
    cmd = "gzip -1 " + req.query.file;
  } else {
    cmd = "gzip -9 " + req.query.file;
  }
  execSync(cmd); // expect: ts/cmdi
  res.end();
});

router.get("/loop", (req, res) => {
  const files = String(req.query.files).split(",");
  let args = "";
  for (const f of files) {
    args = args + " " + f;
  }
  exec("tar czf out.tgz" + args); // expect: ts/cmdi
  res.end();
});

router.get("/try", async (req, res) => {
  try {
    const out = execSync(`du -sh ${req.query.path}`); // expect: ts/cmdi
    res.send(out);
  } catch {
    res.status(500).end();
  }
});

router.post("/destructure", async (req: Request, res: Response) => {
  const { target } = req.body;
  const output = await Promise.resolve(execSync("nslookup " + target)); // expect: ts/cmdi
  res.send(output);
});

router.get("/array", (req, res) => {
  const parts = ["git", "log", "--author"];
  parts.push(String(req.query.author));
  exec(parts.join(" ")); // expect: ts/cmdi
  res.end();
});

router.get("/object", (req, res) => {
  const opts = { dir: req.query.dir };
  exec("ls " + opts.dir); // expect: ts/cmdi
  res.end();
});

router.get("/methods", (req, res) => {
  const name = (req.query.name as string).trim().toLowerCase();
  exec(`useradd ${name}`); // expect: ts/cmdi
  res.end();
});

router.get("/ternary", (req, res) => {
  const level = req.query.verbose ? "-v" : req.query.level;
  exec("run " + level); // expect: ts/cmdi
  res.end();
});

router.get("/switch", (req, res) => {
  switch (req.query.op) {
    case "list":
      exec("ls " + req.query.dir); // expect: ts/cmdi
      break;
    default:
      res.end();
  }
});

router.get("/spawn-command", (req, res) => {
  spawn(req.query.bin as string); // expect: ts/cmdi
  res.end();
});

router.get("/spawn-shell", (req, res) => {
  spawn("sh", ["-c", String(req.query.script)], { shell: true }); // expect: ts/cmdi
  res.end();
});

router.get("/spawnsync-shell", (req, res) => {
  spawnSync("find . -name " + req.query.pattern, { shell: true }); // expect: ts/cmdi
  res.end();
});

router.get("/execfile-command", (req, res) => {
  execFile(req.query.tool as string, ["--version"]); // expect: ts/cmdi
  res.end();
});

router.get("/namespace", (req, res) => {
  cp.exec("whois " + req.query.domain); // expect: ts/cmdi
  res.end();
});

function lookup(req: Request, res: Response) {
  const q = req.query;
  exec("host " + q.name); // expect: ts/cmdi
  res.end();
}
router.get("/named", lookup);

export class ScriptController {
  run(req: Request, res: Response) {
    execSync(`./scripts/${req.params.script}.sh`); // expect: ts/cmdi
    res.end();
  }
}

router.get("/two-templates", (req, res) => {
  const cmd = `echo ${req.query.a}` + ` && echo ${req.query.b}`;
  exec(cmd); // expect: ts/cmdi
  res.end();
});

router.get("/captured", (req, res) => {
  const x = req.query.x as string;
  const read = () => x;
  exec("echo " + x); // expect: ts/cmdi
  res.send(read());
});

router.post("/json", (req, res) => {
  const data = JSON.parse(req.body.json);
  exec("echo " + data.message); // expect: ts/cmdi
  res.end();
});

router.get("/url", (req, res) => {
  const url = new URL(req.url, "http://localhost");
  exec("curl " + url.searchParams.get("next")); // expect: ts/cmdi
  res.end();
});

router.get("/while", (req, res) => {
  let cmd = "echo";
  let i = 0;
  while (i < 3) {
    cmd = cmd + " " + req.query.word;
    i++;
  }
  exec(cmd); // expect: ts/cmdi
  res.end();
});

router.get("/nested-destructure", (req, res) => {
  const {
    query: { file },
  } = req;
  exec("file " + file); // expect: ts/cmdi
  res.end();
});

router.get("/optional", (req, res) => {
  const name = req.query?.name ?? "default";
  exec("id " + name); // expect: ts/cmdi
  res.end();
});

router.post("/annotated", (req, res) => {
  // A type annotation validates nothing: the body can hold any string.
  const count: number = req.body.count;
  exec("seq " + count); // expect: ts/cmdi
  res.end();
});

export default router;
