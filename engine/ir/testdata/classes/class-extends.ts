class Animal {
  constructor(public name: string) {}
  speak() {
    return this.name;
  }
}
class Dog extends Animal {
  constructor(
    name: string,
    public breed: string,
  ) {
    super(name);
  }
  speak() {
    return super.speak() + " woof";
  }
}
