function f(xs: number[]) {
  for (const x of xs) {
    break;
    use(x);
  }
}
declare function use(v: unknown): void;
