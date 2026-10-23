function f() {
  try {
    run();
  } catch ({ name, message }) {
    return name + message;
  }
}
declare function run(): void;
