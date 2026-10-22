function f(i: number) {
  const a = i++;
  const b = ++i;
  const c = i--;
  const d = --i;
  return [a, b, c, d];
}
