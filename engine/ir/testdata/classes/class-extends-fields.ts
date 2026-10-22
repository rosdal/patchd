class Base {
  base = 1;
}
class Derived extends Base {
  extra = this.base + 1;
  constructor() {
    console.log("before");
    super();
    console.log(this.extra);
  }
}
