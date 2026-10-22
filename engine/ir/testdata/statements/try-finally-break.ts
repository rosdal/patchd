function f(xs: number[]) {
  for (const x of xs) {
    try {
      if (x > 1) break;
      if (x < 0) continue;
      use(x);
    } finally {
      done(x);
    }
  }
}
declare function use(v: unknown): void;
declare function done(v: unknown): void;
