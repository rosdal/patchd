function f(o: any) {
  let a, b;
  ({
    x: [a, { b }],
  } = o);
  return [a, b];
}
