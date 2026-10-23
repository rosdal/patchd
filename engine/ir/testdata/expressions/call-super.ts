class A {
  greet() {
    return "a";
  }
}
class B extends A {
  greet() {
    return super.greet() + "b";
  }
}
