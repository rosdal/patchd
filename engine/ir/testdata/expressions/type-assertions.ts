function f(x: unknown) {
  const a = x as string;
  const b = <number>x;
  const c = x!;
  const d = { k: 1 } satisfies Record<string, number>;
  return [a, b, c, d];
}
