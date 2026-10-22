class C {
  #x = 1;
  static has(o: object) {
    return #x in o;
  }
}
