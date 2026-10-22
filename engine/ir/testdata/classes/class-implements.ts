interface Greeter {
  greet(): string;
}
class English implements Greeter {
  greet() {
    return "hello";
  }
}
