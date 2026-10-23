const f = function loop(n: number) {
  return () => loop(n - 1);
};
