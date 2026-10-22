// Classes and this types
class Builder {
  private parts: string[] = [];
  add(part: string): this {
    this.parts.push(part);
    return this;
  }
  build(): string {
    return this.parts.join("");
  }
}
class LoudBuilder extends Builder {
  shout(): this {
    return this;
  }
}
const built = new LoudBuilder().add("a").shout();
//    ^? LoudBuilder
const out = built.build();
//    ^? string
export { out };
