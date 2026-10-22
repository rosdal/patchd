// Generics and inference
export function identity<T>(value: T): T {
  return value;
}
const n = identity(42);
//    ^? 42
const s = identity("hi");
//    ^? "hi"
const pair = [identity(true), identity([1, 2])] as const;
//    ^? readonly [true, number[]]
function first<T extends readonly unknown[]>(xs: T): T[0] {
  return xs[0];
}
const f = first(["a", 1] as const);
//    ^? "a"
let widened = identity<number>(42);
//  ^? number
widened = 1;
export const results = { n, s, pair, f, widened };
