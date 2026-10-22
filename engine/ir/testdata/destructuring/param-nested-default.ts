function f({ a: { b } = { b: 1 } }: any) {
  return b;
}
