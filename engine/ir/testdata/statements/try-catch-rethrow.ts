function f() {
  try {
    run();
  } catch (e) {
    if (e instanceof TypeError) return;
    throw e;
  }
}
declare function run(): void;
