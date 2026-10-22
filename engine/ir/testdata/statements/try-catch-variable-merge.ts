function f() {
  let stage = "start";
  try {
    stage = "read";
    read();
    stage = "parse";
    parse();
    stage = "done";
  } catch {
    report(stage);
  }
  return stage;
}
declare function read(): void;
declare function parse(): void;
declare function report(s: string): void;
