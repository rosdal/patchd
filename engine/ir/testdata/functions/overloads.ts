function parse(x: string): number;
function parse(x: number): number;
function parse(x: any) {
  return Number(x);
}
