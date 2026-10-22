function outer() {
  return inner(1);
  function inner(n: number) {
    return n + 1;
  }
}
