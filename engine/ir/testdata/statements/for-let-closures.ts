function f() {
  const fns: (() => number)[] = [];
  for (let i = 0; i < 3; i++) {
    fns.push(() => i);
  }
  return fns;
}
