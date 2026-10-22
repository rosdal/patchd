function f() {
  let x = 1;
  {
    let x = 2;
    use(x);
  }
  return x;
}
declare function use(v: unknown): void;
