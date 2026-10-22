function f(o: any) {
  const { a: { b = 1 } = {} } = o;
  return b;
}
