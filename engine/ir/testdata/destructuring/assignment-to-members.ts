function f(o: any, xs: number[]) {
  [o.first, o["second"]] = xs;
  ({ a: o.a } = { a: 1 });
}
