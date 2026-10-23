export class User {
  constructor(public name: string) {}
  greet() {
    return "hi " + this.name;
  }
}
