function f(o: any) {
  let x, y;
  ({ a: x = 1, b: y } = o);
  return [x, y];
}
