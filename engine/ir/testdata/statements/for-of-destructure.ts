function f(entries: [string, number][]) {
  let total = 0;
  for (const [, n] of entries) total += n;
  return total;
}
