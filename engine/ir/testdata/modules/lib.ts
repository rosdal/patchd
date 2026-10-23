export const version = "1.0";
export let counter = 0;
export function bump() {
  counter++;
}
export default class Client {
  run() {
    return version;
  }
}
export enum Mode {
  A,
}
export namespace Tools {
  export const id = 1;
}
