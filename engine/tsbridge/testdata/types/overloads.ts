// Overloads: the chosen overload decides the type
function parse(input: string): number;
function parse(input: number): string;
function parse(input: string | number): number | string {
  return typeof input === "string" ? Number(input) : String(input);
}
const a = parse("1");
//    ^? number
const b = parse(1);
//    ^? string
export const both = { a, b };
