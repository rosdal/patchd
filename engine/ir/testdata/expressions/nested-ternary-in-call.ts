function f(a: boolean, g: (x: number) => void) {
  g(a ? 1 : 2);
}
