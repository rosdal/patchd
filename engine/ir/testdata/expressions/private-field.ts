class Counter {
  #n = 0;
  inc() {
    this.#n++;
    return this.#n;
  }
}
