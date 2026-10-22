function* all(a: Iterable<number>, b: Iterable<number>) {
  yield* a;
  yield* b;
}
