class Base {
  get label() {
    return "base";
  }
  static kind = "b";
}
class Child extends Base {
  get label() {
    return super.label + "/child";
  }
  static describe() {
    return super.kind;
  }
  tag() {
    const read = () => super.label;
    return read();
  }
}
