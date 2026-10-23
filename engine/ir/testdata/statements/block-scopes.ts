function f() {
  {
    const a = 1;
    use(a);
  }
  {
    const a = 2;
    use(a);
  }
}
declare function use(v: unknown): void;
