namespace Util {
  export const version = "1";
  export function greet(n: string) {
    return `hi ${n} v${version}`;
  }
  const hidden = 2;
  export const sum = hidden + 1;
}
