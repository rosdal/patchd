function f() {
  try {
    run();
  } catch ({ message, code = 500 }) {
    return { message, code };
  }
}
declare function run(): void;
