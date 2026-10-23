function f(c: boolean) {
  if (c) {
    function g() {
      return 1;
    }
    return g();
  }
  return 0;
}
