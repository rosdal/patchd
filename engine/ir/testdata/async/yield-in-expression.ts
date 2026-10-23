function* g() {
  const a = (yield 1) + (yield 2);
  return a;
}
