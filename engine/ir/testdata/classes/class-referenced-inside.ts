class Singleton {
  static instance?: Singleton;
  static get() {
    return (Singleton.instance ??= new Singleton());
  }
}
