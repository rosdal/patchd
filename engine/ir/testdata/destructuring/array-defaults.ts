function f(xs: number[]) {
  const [a = 1, b = a] = xs;
  return b;
}
