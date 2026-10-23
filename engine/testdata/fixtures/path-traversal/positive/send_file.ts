// res.sendFile and res.download, and flows into them.
import express, { type Request, type Response, type NextFunction } from "express";
import path from "node:path";

const app = express();

app.get("/files/:file", (req, res) => {
  res.sendFile(path.resolve("public", req.params.file)); // expect: ts/path-traversal
});

app.get("/download", (req, res) => {
  res.download(req.query.f as string); // expect: ts/path-traversal
});

// The shape of OWASP Juice Shop's key server: a check that only rejects
// forward slashes does not confine the path.
export function serveKeyFiles() {
  return ({ params }: Request, res: Response, next: NextFunction) => {
    const file = params.file;
    if (!file.includes("/")) {
      res.sendFile(path.resolve("encryptionkeys/", file)); // expect: ts/path-traversal from=19
    } else {
      res.status(403);
      next(new Error("File names cannot contain forward slashes!"));
    }
  };
}

app.get("/decoded/:p", (req, res) => {
  const p = decodeURIComponent(req.params.p);
  res.sendFile(path.join(__dirname, "static", p)); // expect: ts/path-traversal
});

app.get("/half-sanitised", (req, res) => {
  // basename on the directory only: the file name still climbs out.
  res.sendFile(path.join("/data", path.basename(String(req.query.dir)), String(req.query.file))); // expect: ts/path-traversal
});

app.get("/checked-later", (req, res) => {
  // startsWith after resolve is a real check, but a branch: v0.4 still
  // reports it (needs path-sensitive analysis; see the rule docs).
  const file = path.resolve("/srv/public", String(req.query.f));
  if (!file.startsWith("/srv/public/")) {
    res.sendStatus(403);
    return;
  }
  res.sendFile(file); // expect: ts/path-traversal
});

app.get("/template", (req, res) => {
  res.download(`reports/${req.query.year}/${req.query.name}.pdf`); // expect: ts/path-traversal
});

app.get("/loop", (req, res) => {
  let p = "/srv";
  for (const part of String(req.query.parts).split("/")) {
    p = path.join(p, part);
  }
  res.sendFile(p); // expect: ts/path-traversal
});
