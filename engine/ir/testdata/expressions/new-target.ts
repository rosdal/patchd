function F(this: any) {
  if (!new.target) return new (F as any)();
  this.made = true;
}
