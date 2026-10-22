function f(a: number, rest: number[]) {
  return [a, , a + 1, ...rest];
}
