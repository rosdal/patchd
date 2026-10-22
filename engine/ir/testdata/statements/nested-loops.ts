function f(n: number) {
  let c = 0;
  for (let i = 0; i < n; i++) {
    for (let j = 0; j < i; j++) {
      c += i * j;
    }
  }
  return c;
}
