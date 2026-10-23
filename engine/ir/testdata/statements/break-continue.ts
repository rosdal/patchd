function f(xs: number[]) {
  let n = 0;
  for (const x of xs) {
    if (x < 0) continue;
    if (x > 100) break;
    n += x;
  }
  return n;
}
