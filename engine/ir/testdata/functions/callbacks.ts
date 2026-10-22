function f(xs: number[]) {
  return xs
    .filter((x) => x > 0)
    .map((x) => x * 2)
    .reduce((a, b) => a + b, 0);
}
