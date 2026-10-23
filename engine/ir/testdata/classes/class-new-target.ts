class Base {
  kind: string;
  constructor() {
    this.kind = new.target.name;
  }
}
