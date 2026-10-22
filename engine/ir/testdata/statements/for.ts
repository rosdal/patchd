function sum(xs: number[]) {
  let s = 0;
  for (let i = 0; i < xs.length; i++) {
    s += xs[i];
  }
  return s;
}
