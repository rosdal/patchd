// Declaration merging
interface Box {
  width: number;
}
interface Box {
  height: number;
}
declare const box: Box;
const h = box.height;
//    ^? number
namespace Shapes {
  export const sides = 4;
}
namespace Shapes {
  export const name = "square";
}
const sides = Shapes.sides;
//    ^? 4
export { h, sides };
