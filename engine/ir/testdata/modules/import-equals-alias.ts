namespace Outer {
  export namespace Inner {
    export const v = 1;
  }
}
import V = Outer.Inner;
export const x = V.v;
