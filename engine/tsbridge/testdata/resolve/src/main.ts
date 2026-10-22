import express from "express";
import { exec } from "node:child_process";
import * as fs from "fs";
import { PrismaClient } from "@prisma/client";
import { query, Repo } from "./index.js";
import { runQuery as aliased } from "@app/db.js";
import Default from "./app/db.js";
import missing from "not-installed";
import untyped from "untyped-lib";

const app = express(); // resolves-to: express:default
app.get("/", (req, res) => {
  res.redirect(req.query.next); // resolves-to: express:Response.redirect
  res.send("ok").send("again"); // resolves-to: express:Response.send
});
app.use(express.json()); // resolves-to: express:Express.use

exec("ls"); // resolves-to: node:child_process.exec
fs.readFileSync("a", "utf8"); // resolves-to: node:fs.readFileSync
JSON.parse("{}"); // resolves-to: global:JSON.parse
encodeURIComponent("x"); // resolves-to: global:encodeURIComponent

const prisma = new PrismaClient(); // resolves-to: @prisma/client:PrismaClient
prisma.$queryRawUnsafe("select 1"); // resolves-to: @prisma/client:PrismaClient.$queryRawUnsafe

query("select 2"); // resolves-to: local:src/app/db.ts#runQuery
aliased("select 3"); // resolves-to: local:src/app/db.ts#runQuery
new Repo().find(1); // resolves-to: local:src/app/db.ts#default.find
new Default(); // resolves-to: local:src/app/db.ts#default

function sql(strings: TemplateStringsArray, ...values: unknown[]) {
  return strings.join("?") + values.length;
}
sql`select ${1}`; // resolves-to: local:src/main.ts#sql

interface Store {
  save(): void;
}
class FileStore implements Store {
  save() {}
}
class MemoryStore implements Store {
  save() {}
}
declare const store: Store;
store.save(); // resolves-to: local:src/main.ts#Store.save
declare const either: FileStore | MemoryStore;
either.save(); // declarations: 2

declare const anything: any;
anything.run(); // unresolved: any-receiver
const handlers: Record<string, () => void> = {};
declare const key: string;
handlers[key](); // unresolved: dynamic-property
missing(); // unresolved: package-not-installed
untyped.run(); // unresolved: untyped-package
undeclaredGlobal.go(); // unresolved: unresolved-name
