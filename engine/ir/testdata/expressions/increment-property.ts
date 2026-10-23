function f(o: { count: number }) {
  o.count++;
  return ++o.count;
}
