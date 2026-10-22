const fact = function self(n: number): number {
  return n <= 1 ? 1 : n * self(n - 1);
};
