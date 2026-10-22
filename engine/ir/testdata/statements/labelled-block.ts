function f(c: boolean) {
  let x = 1;
  block: {
    if (c) break block;
    x = 2;
  }
  return x;
}
