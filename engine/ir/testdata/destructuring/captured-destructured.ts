function f(o: any) {
  const { a, b } = o;
  return () => a + b;
}
