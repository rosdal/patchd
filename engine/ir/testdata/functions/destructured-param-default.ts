function f({ a = 1, b }: { a?: number; b: number } = { b: 2 }) {
  return a + b;
}
