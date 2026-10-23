function f(o: any) {
  let a;
  ({ a = 5 } = o);
  return a;
}
