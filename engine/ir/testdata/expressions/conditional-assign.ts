function f(c: boolean) {
  const x = c ? load() : null;
  return x;
}
declare function load(): string;
