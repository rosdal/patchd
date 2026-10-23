import lib, { shared as s } from "./lib";
import type { Missing } from "./nowhere";
export { s };

let counter = 0;
const label = "a\tb";
const re = /ab+c/gi;
const tpl = `x${counter}y`;

export class Box extends Array<number> {
  static count = 0;
  #secret = 1;
  async *items() {
    yield* [];
  }
}

export function use(o: any, a: number, xs: number[]) {
  counter++;
  ++counter;
  const sum = a + counter;
  const short = { a };
  const chain = o?.b.c;
  for (let i = 0; i < 1; i++) {}
  const { p: q = 1, r } = o;
  return [sum, short, chain, q, r, lib(), s, label, re, tpl, xs];
}

export async function stream(src: AsyncIterable<number>) {
  for await (const x of src) console.log(x, mystery);
}
