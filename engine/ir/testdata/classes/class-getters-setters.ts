class Temp {
  #c = 0;
  get f() {
    return this.#c * 1.8 + 32;
  }
  set f(v: number) {
    this.#c = (v - 32) / 1.8;
  }
  static get zero() {
    return new Temp();
  }
}
