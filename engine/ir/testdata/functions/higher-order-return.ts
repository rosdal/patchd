function compose(f: (x: number) => number, g: (x: number) => number) {
  return (x: number) => f(g(x));
}
