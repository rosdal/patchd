function Point(this: any, x: number) {
  this.x = x;
}
const p = new (Point as any)(1);
