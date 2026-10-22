abstract class Shape {
  abstract area(): number;
  describe() {
    return `area ${this.area()}`;
  }
}
class Square extends Shape {
  constructor(private s: number) {
    super();
  }
  area() {
    return this.s * this.s;
  }
}
