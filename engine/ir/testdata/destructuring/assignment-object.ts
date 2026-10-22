function f(o: any) {
  let a, b;
  ({ a, b } = o);
  return [a, b];
}
