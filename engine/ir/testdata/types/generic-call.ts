function first<T>(xs: T[]): T | undefined {
  return xs[0];
}
export const n = first([1, 2, 3]);
