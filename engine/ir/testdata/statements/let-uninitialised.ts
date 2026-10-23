function f(xs: number[]) {
  for (const x of xs) {
    let seen;
    if (x > 0) seen = x;
    use(seen);
  }
}
declare function use(v: unknown): void;
