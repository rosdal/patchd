// File access with paths that don't come from a request.
import * as fs from "node:fs";
import path from "node:path";

export function loadTemplate(name: string) {
  return fs.readFileSync(path.join(__dirname, "templates", name)); // expect-none
}

export function cleanup(dir: string) {
  for (const f of fs.readdirSync(dir)) { // expect-none
    fs.unlinkSync(path.join(dir, f)); // expect-none
  }
}

const FILES = ["a.txt", "b.txt"];
export function readAll() {
  return FILES.map((f) => fs.readFileSync(f)); // expect-none
}

export async function config() {
  return fs.promises.readFile("config.json", "utf8"); // expect-none
}
