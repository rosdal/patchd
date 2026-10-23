// Commands built from values that are not request input.
import { exec, execSync } from "node:child_process";
import type { Request } from "express";

export function backup(dir: string) {
  exec("tar czf backup.tgz " + dir); // expect-none
}

export function deploy(env: "staging" | "production") {
  execSync(`./deploy.sh ${env}`); // expect-none
}

export function withRequestType(req: Request) {
  const id = req.query.id;
  execSync("echo done"); // expect-none
  return id;
}

export function counter(start: number) {
  for (let i = start; i < start + 3; i++) {
    exec("echo " + i); // expect-none
  }
}

const config = { tool: "eslint" };
export function lint() {
  exec(config.tool + " ."); // expect-none
}
