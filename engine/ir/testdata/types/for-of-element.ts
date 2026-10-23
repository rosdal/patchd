export function f(m: Map<string, Date>) {
  for (const [k, d] of m) console.log(k, d.getTime());
}
