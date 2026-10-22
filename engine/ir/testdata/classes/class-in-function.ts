function make(prefix: string) {
  class Local {
    say(x: string) {
      return prefix + x;
    }
  }
  return new Local();
}
