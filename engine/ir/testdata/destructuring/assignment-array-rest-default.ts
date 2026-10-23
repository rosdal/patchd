function f(xs: number[]) {
  let a, rest;
  [a = 0, ...rest] = xs;
  return rest;
}
