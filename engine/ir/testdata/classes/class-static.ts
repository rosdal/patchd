class Registry {
  static items: string[] = [];
  static count = 0;
  static add(x: string) {
    Registry.items.push(x);
    this.count++;
  }
}
