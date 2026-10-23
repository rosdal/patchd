class Point {
  constructor(
    public x: number,
    public y: number,
  ) {}
  len() {
    return Math.hypot(this.x, this.y);
  }
}
