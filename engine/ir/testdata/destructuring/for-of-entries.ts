function f(m: Map<string, number>) {
  let total = 0;
  for (const [k, v] of m) total += v + k.length;
  return total;
}
