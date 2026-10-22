function f(xs: number[]) {
  const [head, ...tail] = xs;
  return tail.length + head;
}
