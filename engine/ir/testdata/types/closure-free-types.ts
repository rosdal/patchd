export function f(prefix: string) {
  let count = 0;
  return () => prefix + count++;
}
