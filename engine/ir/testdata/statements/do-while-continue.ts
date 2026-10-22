function f(xs: number[]) {
  let i = 0;
  do {
    i++;
    if (xs[i] < 0) continue;
    use(xs[i]);
  } while (i < xs.length);
}
declare function use(v: unknown): void;
