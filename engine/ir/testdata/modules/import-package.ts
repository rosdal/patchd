import express from "express";
import { readFile } from "node:fs";
const app = express();
app.get("/", (req: any, res: any) => readFile(req.query.f, () => res.end()));
