class Env {
  static vars: Record<string, string>;
  static {
    Env.vars = { mode: "dev" };
    const local = 1;
    console.log(local);
  }
}
