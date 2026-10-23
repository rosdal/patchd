class Vault {
  #secret = "s";
  #reveal() {
    return this.#secret;
  }
  open() {
    return this.#reveal();
  }
}
