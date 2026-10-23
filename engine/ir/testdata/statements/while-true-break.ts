function f(q: string[]) {
  while (true) {
    const x = q.pop();
    if (!x) break;
    use(x);
  }
}
declare function use(v: unknown): void;
