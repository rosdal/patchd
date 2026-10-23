function f(a: number, b: number) {
  [a, b] = [b, a];
  return [a, b];
}
